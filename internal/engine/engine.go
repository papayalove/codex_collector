package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"codex-collector/internal/identity"
)

const warnFileName = "git仓库存储90%.md"

type Engine struct {
	DataDir   string
	ConfigDir string
	Git       gitRunner
	Now       func() time.Time
	Home      string
	AfterPush func()
	Log       io.Writer

	mu    sync.Mutex
	locks map[string]*sync.Mutex
	logMu sync.Mutex
}

func (e *Engine) configDir() string {
	if e.ConfigDir != "" {
		return e.ConfigDir
	}
	return e.DataDir
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Engine) lock(key string) *sync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.locks == nil {
		e.locks = map[string]*sync.Mutex{}
	}
	lk := e.locks[key]
	if lk == nil {
		lk = &sync.Mutex{}
		e.locks[key] = lk
	}
	return lk
}

type hookEvent struct {
	Hook       string `json:"hook_event_name"`
	SessionID  string `json:"session_id"`
	TurnID     string `json:"turn_id"`
	CWD        string `json:"cwd"`
	Transcript string `json:"transcript_path"`
	Model      string `json:"model"`
}

func (e *Engine) Handle(raw []byte) Result {
	var ev hookEvent
	if json.Unmarshal(raw, &ev) != nil {
		return Result{Action: "skip", Detail: "事件无法解析"}
	}
	cfg := loadConfig(e.configDir())
	if !cfg.Consent {
		return Result{Action: "skip", Detail: "未同意采集"}
	}
	switch ev.Hook {
	case "SessionStart":
		return e.onStart(ev, cfg)
	case "Stop":
		return e.onStop(ev, cfg, false)
	case "SessionEnd":
		return e.onStop(ev, cfg, true)
	case "Interrupt":
		appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), SessionID: ev.SessionID, TurnID: ev.TurnID, Reason: reasonIdle, Detail: "中断"})
		return Result{Action: "skip", Detail: "已记录中断"}
	default:
		return Result{Action: "skip"}
	}
}

func (e *Engine) identity(cfg Config) (string, string) {
	if id, source := identity.Resolve(cfg.Email, "", ""); source == "email" {
		return id, source
	}
	home := e.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	if id, source := identity.Resolve("", identity.AccountID(codexHome), ""); source == "account" {
		return id, source
	}
	return identity.Resolve("", "", identity.DeviceID())
}

func (e *Engine) onStart(ev hookEvent, cfg Config) Result {
	root, skip := snapshotRoot(ev.CWD)
	if skip != "" {
		appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), SessionID: ev.SessionID, Reason: reasonSkipped, Detail: skip})
		return Result{Action: "record", Detail: skip}
	}
	userID, source := e.identity(cfg)
	key := repoKey(userID, root)
	gitDir := filepath.Join(e.DataDir, "repos", key, ".git")
	if err := e.Git.init(gitDir); err != nil {
		appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, SessionID: ev.SessionID, Reason: reasonFailed, Detail: err.Error()})
		return Result{Action: "record", Detail: "仓库初始化失败"}
	}
	st := loadState(e.statePath(key))
	if len(st.Files) == 0 {
		meta, stop, err := scanMeta(filepath.FromSlash(root), cfg.MaxSnapshotFiles, cfg.MaxFileBytes)
		if err != nil {
			appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, SessionID: ev.SessionID, Reason: reasonFailed, Detail: err.Error()})
			return Result{Action: "record", Detail: "扫描失败"}
		}
		if stop != "" {
			appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, SessionID: ev.SessionID, Reason: reasonSkipped, Detail: stop})
			return Result{Action: "record", Detail: stop}
		}
		st.Files = filesFromMeta(meta)
	}
	st.RepoKey = key
	st.ProjectPath = root
	st.UserID = userID
	st.UserIDSource = source
	_ = st.save(e.statePath(key))
	if cfg.EnterpriseNamespace != "" {
		go e.startRemote(cfg, gitDir, key)
	}
	e.record(key, ev.SessionID, "", reasonStart, source)
	return Result{Action: "ready", Detail: source}
}

// startRemote prepares the empty remote, then retries one push that already
// failed or already reached the batch size. Session start is the chance to
// send it: the idle commit loop does not look at unpushed commits once the
// queue is empty.
func (e *Engine) startRemote(cfg Config, gitDir, key string) {
	e.ensureRemoteLater(cfg, gitDir, key)
	e.retryPendingPush(cfg, gitDir, key)
}

func (e *Engine) retryPendingPush(cfg Config, gitDir, key string) {
	cfg = cfg.normalized()
	if cfg.EnterpriseNamespace == "" {
		return
	}
	lk := e.lock(key)
	lk.Lock()
	st := loadState(e.statePath(key))
	branch, count := pendingPush(st, cfg.PushEveryN)
	if branch == "" || st.Pushing || st.Paused {
		lk.Unlock()
		return
	}
	lk.Unlock()

	if err := e.ensureEnterpriseRepo(cfg, gitDir, key); err != nil {
		appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, Reason: reasonFailed, Detail: err.Error()})
		e.reportPush("", err)
		return
	}

	lk.Lock()
	st = loadState(e.statePath(key))
	branch, count = pendingPush(st, cfg.PushEveryN)
	if branch == "" || st.Pushing || st.Paused {
		lk.Unlock()
		return
	}
	st.Pushing = true
	_ = st.save(e.statePath(key))
	lk.Unlock()
	e.push(key, gitDir, branch, count)
}

// pendingPush is a branch that should be sent without waiting for another commit.
// A failed push is retried even during its backoff. A full batch that never
// started is sent as well. A short batch that has not failed stays local.
func pendingPush(st repoState, every int) (string, int) {
	names := make([]string, 0, len(st.Branches))
	for name := range st.Branches {
		names = append(names, name)
	}
	sort.Strings(names)
	failedName, failedCount := "", 0
	for _, name := range names {
		br := st.Branches[name]
		if br.Unpushed <= 0 || br.Commit == "" {
			continue
		}
		if every > 0 && br.Unpushed >= every {
			return name, br.Unpushed
		}
		if st.PushFails > 0 && failedName == "" {
			failedName, failedCount = name, br.Unpushed
		}
	}
	return failedName, failedCount
}

func (e *Engine) onStop(ev hookEvent, cfg Config, sessionEnd bool) Result {
	root, skip := snapshotRoot(ev.CWD)
	if skip != "" {
		appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), SessionID: ev.SessionID, TurnID: ev.TurnID, Reason: reasonSkipped, Detail: skip})
		return Result{Action: "record", Detail: skip}
	}
	userID, source := e.identity(cfg)
	key := repoKey(userID, root)
	lk := e.lock(key)
	lk.Lock()
	defer lk.Unlock()

	gitDir := filepath.Join(e.DataDir, "repos", key, ".git")
	if err := e.Git.init(gitDir); err != nil {
		appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, SessionID: ev.SessionID, TurnID: ev.TurnID, Reason: reasonFailed, Detail: err.Error()})
		return Result{Action: "record", Detail: "仓库初始化失败"}
	}
	st := loadState(e.statePath(key))
	if st.Paused {
		appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, SessionID: ev.SessionID, TurnID: ev.TurnID, Reason: reasonSkipped, Detail: "仓库已到 20 GiB"})
		return Result{Action: "record", Detail: "仓库已暂停"}
	}
	meta, stop, err := scanMeta(filepath.FromSlash(root), cfg.MaxSnapshotFiles, cfg.MaxFileBytes)
	if err != nil {
		appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, SessionID: ev.SessionID, TurnID: ev.TurnID, Reason: reasonFailed, Detail: err.Error()})
		return Result{Action: "record", Detail: "扫描失败"}
	}
	if stop != "" {
		appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, SessionID: ev.SessionID, TurnID: ev.TurnID, Reason: reasonSkipped, Detail: stop})
		return Result{Action: "record", Detail: stop}
	}
	changed := metadataChanged(st.Files, meta)
	var trajectories []trajFile
	var tlen int64
	loadTrajectory := func() {
		trajectories = e.collectTrajectories(ev, key)
		for _, file := range trajectories {
			tlen += int64(len(file.Body))
		}
	}
	if !changed {
		if !sessionEnd {
			appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, SessionID: ev.SessionID, TurnID: ev.TurnID, Reason: reasonIdle, Detail: "没有文件改动"})
			return Result{Action: "skip", Detail: "没有文件改动"}
		}
		loadTrajectory()
		if tlen == st.TranscriptLen || tlen == 0 {
			appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, SessionID: ev.SessionID, TurnID: ev.TurnID, Reason: reasonIdle, Detail: "没有文件改动"})
			return Result{Action: "skip", Detail: "没有文件改动"}
		}
	} else {
		loadTrajectory()
	}
	branch, head := userBranch(root)
	tree, files, err := e.buildTree(gitDir, root, st.Files, meta, trajectories, ev, branch, head, source, userID, cfg.MaxFileBytes)
	if err != nil {
		appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, SessionID: ev.SessionID, TurnID: ev.TurnID, Reason: reasonFailed, Detail: err.Error()})
		return Result{Action: "record", Detail: "记树失败"}
	}
	jobs := loadJobs(e.queuePath(key))
	turn := ev.TurnID
	if turn == "" {
		turn = "session-end"
	}
	jobs = append(jobs, Job{Tree: tree, Branch: branch, TurnIDs: []string{turn}, SessionID: ev.SessionID, UserHead: head})
	if err := saveJobs(e.queuePath(key), jobs); err != nil {
		appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, Reason: reasonFailed, Detail: err.Error()})
		return Result{Action: "record", Detail: "排队失败"}
	}
	st.RepoKey = key
	st.ProjectPath = root
	st.UserID = userID
	st.UserIDSource = source
	st.Files = files
	st.TranscriptLen = tlen
	_ = st.save(e.statePath(key))
	e.record(key, ev.SessionID, turn, reasonQueued, branch)
	return Result{Action: "sealed", Detail: branch}
}

func filesFromMeta(items []scanned) map[string]fileState {
	files := make(map[string]fileState, len(items))
	for _, it := range items {
		files[it.rel] = fileState{Size: it.size, Mtime: it.mtime, Mode: it.mode, Kind: it.kind}
	}
	return files
}

func metadataChanged(prev map[string]fileState, items []scanned) bool {
	seen := map[string]bool{}
	for _, it := range items {
		seen[it.rel] = true
		old, ok := prev[it.rel]
		if !ok || old.Size != it.size || old.Mtime != it.mtime {
			return true
		}
	}
	for path := range prev {
		if strings.HasPrefix(path, ".codex-collect/") {
			continue
		}
		if !seen[path] {
			return true
		}
	}
	return false
}

func redact(b []byte) []byte {
	lines := bytes.Split(b, []byte("\n"))
	for i, line := range lines {
		if secretLine.Match(line) {
			lines[i] = []byte("[REDACTED]")
		}
	}
	return bytes.Join(lines, []byte("\n"))
}

func (e *Engine) buildTree(gitDir, project string, prev map[string]fileState, items []scanned, trajectories []trajFile, ev hookEvent, branch, head, source, userID string, maxBytes int64) (string, map[string]fileState, error) {
	files := map[string]fileState{}
	var info []byte
	var toHash []scanned
	host, _ := os.Hostname()
	var manifest []manifestItem
	for _, it := range items {
		if old, ok := prev[it.rel]; ok && old.Size == it.size && old.Mtime == it.mtime && (old.Blob != "" || old.Kind == "large") {
			files[it.rel] = old
			if old.Kind == "large" {
				manifest = append(manifest, manifestItem{Path: it.rel, Remote: host + ":" + it.abs, Reason: "大文件"})
				continue
			}
			mode := old.Mode
			if mode == "" {
				mode = it.mode
			}
			info = append(info, indexInfo(mode, old.Blob, it.rel)...)
			if old.Kind == "secret" {
				manifest = append(manifest, manifestItem{Path: it.rel, Remote: host + ":" + it.abs, Reason: "因密钥排除"})
			}
			continue
		}
		it = classifyContent(it, maxBytes)
		switch it.kind {
		case "large":
			files[it.rel] = fileState{Size: it.size, Mtime: it.mtime, Mode: "100644", Kind: "large"}
			manifest = append(manifest, manifestItem{Path: it.rel, Remote: host + ":" + it.abs, Reason: "大文件"})
			continue
		case "secret":
			body := "因密钥排除\n" + it.rel + "\n"
			sha, err := e.Git.hashStdin(gitDir, []byte(body))
			if err != nil {
				return "", nil, err
			}
			info = append(info, indexInfo("100644", sha, it.rel)...)
			files[it.rel] = fileState{Size: it.size, Mtime: it.mtime, Blob: sha, Mode: "100644", Kind: it.kind}
			manifest = append(manifest, manifestItem{Path: it.rel, Remote: host + ":" + it.abs, Reason: "因密钥排除"})
		case "symlink":
			sha, err := e.Git.hashStdin(gitDir, []byte(it.link))
			if err != nil {
				return "", nil, err
			}
			info = append(info, indexInfo("120000", sha, it.rel)...)
			files[it.rel] = fileState{Size: it.size, Mtime: it.mtime, Blob: sha, Mode: "120000", Kind: "symlink"}
		default:
			toHash = append(toHash, it)
		}
	}
	if len(toHash) > 0 {
		paths := make([]string, len(toHash))
		for i, it := range toHash {
			paths[i] = it.abs
		}
		shas, err := e.Git.hashFiles(gitDir, paths)
		if err != nil {
			return "", nil, err
		}
		for i, it := range toHash {
			info = append(info, indexInfo(it.mode, shas[i], it.rel)...)
			files[it.rel] = fileState{Size: it.size, Mtime: it.mtime, Blob: shas[i], Mode: it.mode, Kind: "file"}
		}
	}
	mb, _ := json.Marshal(manifest)
	msha, err := e.Git.hashStdin(gitDir, mb)
	if err != nil {
		return "", nil, err
	}
	info = append(info, indexInfo("100644", msha, ".codex-collect/manifest.json")...)
	repoDir := filepath.Join(e.DataDir, "repos", repoKey(userID, project))
	_ = os.MkdirAll(repoDir, 0o755)
	_ = os.WriteFile(filepath.Join(repoDir, "manifest.json"), mb, 0o644)
	var trajectoryNames, trajectorySources []string
	for _, file := range trajectories {
		trajectoryNames = append(trajectoryNames, file.Name)
		trajectorySources = append(trajectorySources, file.Source)
	}
	meta := map[string]any{
		"userId": userID, "userIdSource": source, "projectPath": project,
		"sessionId": ev.SessionID, "turnId": ev.TurnID, "model": ev.Model,
		"branch": branch, "userHead": head, "frozenAt": e.now().Format(time.RFC3339),
		"codexVersion": e.codexVersion(), "trajectories": trajectoryNames, "trajectorySources": trajectorySources,
	}
	metab, _ := json.Marshal(meta)
	metasha, err := e.Git.hashStdin(gitDir, metab)
	if err != nil {
		return "", nil, err
	}
	info = append(info, indexInfo("100644", metasha, ".codex-collect/meta/"+safeName(ev.TurnID)+".json")...)
	if len(trajectories) > 0 {
		trajDir := filepath.Join(e.DataDir, "repos", repoKey(userID, project), "trajectory")
		_ = os.MkdirAll(trajDir, 0o755)
		for _, file := range trajectories {
			tsha, err := e.Git.hashStdin(gitDir, file.Body)
			if err != nil {
				return "", nil, err
			}
			info = append(info, indexInfo("100644", tsha, ".codex-collect/trajectory/"+file.Name)...)
			_ = os.WriteFile(filepath.Join(trajDir, file.Name), file.Body, 0o644)
		}
	}
	indexPath := filepath.Join(e.DataDir, "index", repoKey(userID, project)+"-"+safeName(ev.TurnID))
	tree, err := e.Git.writeTree(gitDir, indexPath, info)
	_ = os.Remove(indexPath)
	if err != nil {
		return "", nil, err
	}
	return tree, files, nil
}

func safeName(s string) string {
	if s == "" {
		return "unknown"
	}
	s = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' {
			return '-'
		}
		return r
	}, s)
	return s
}

func (e *Engine) statePath(key string) string {
	return filepath.Join(e.DataDir, "state", key+".json")
}

func (e *Engine) queuePath(key string) string {
	return filepath.Join(e.DataDir, "queue", key+".json")
}

func loadJobs(path string) []Job {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var jobs []Job
	_ = json.Unmarshal(b, &jobs)
	return jobs
}

func saveJobs(path string, jobs []Job) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// ClearStalePush drops the in-progress push flag. That flag is only meaningful
// while this process has a push goroutine. A daemon that exits during git push
// otherwise leaves it set, and every later commit is skipped.
func (e *Engine) ClearStalePush() {
	dir := filepath.Join(e.DataDir, "state")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		key := strings.TrimSuffix(ent.Name(), ".json")
		lk := e.lock(key)
		lk.Lock()
		st := loadState(e.statePath(key))
		if st.Pushing {
			st.Pushing = false
			_ = st.save(e.statePath(key))
		}
		lk.Unlock()
	}
}

// CommitAll writes sealed trees that are still queued. It does not read the project again.
func (e *Engine) CommitAll(cfg Config) {
	cfg = cfg.normalized()
	qdir := filepath.Join(e.DataDir, "queue")
	entries, err := os.ReadDir(qdir)
	if err != nil {
		return
	}
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		key := strings.TrimSuffix(ent.Name(), ".json")
		e.commitRepo(key, cfg)
	}
}

func (e *Engine) commitRepo(key string, cfg Config) {
	lk := e.lock(key)
	lk.Lock()
	defer lk.Unlock()
	st := loadState(e.statePath(key))
	if st.Pushing || st.Paused {
		return
	}
	if !st.NextPushAt.IsZero() && e.now().Before(st.NextPushAt) && st.PushFails > 0 && len(loadJobs(e.queuePath(key))) == 0 {
		return
	}
	jobs := loadJobs(e.queuePath(key))
	if len(jobs) == 0 {
		return
	}
	gitDir := filepath.Join(e.DataDir, "repos", key, ".git")
	size, err := e.Git.countBytes(gitDir)
	warnNow := false
	if err == nil {
		warnAt := int64(float64(cfg.RepoLimitBytes) * cfg.RepoWarnRatio)
		if size >= warnAt && !st.Warned {
			appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, Reason: reasonWarn, Bytes: size, Detail: "达到 20 GiB 的 90%"})
			st.Warned = true
			warnNow = true
		}
		if size < warnAt {
			st.Warned = false
		}
		if size >= cfg.RepoLimitBytes {
			st.Paused = true
			branch := ""
			if warnNow {
				branch = e.writeWarnFile(key, gitDir, &st, "", size, cfg.RepoLimitBytes)
			}
			_ = st.save(e.statePath(key))
			appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, Reason: reasonSkipped, Bytes: size, Detail: "仓库已到 20 GiB"})
			if branch != "" {
				e.maybePush(key, gitDir, &st, cfg, branch)
			}
			return
		}
	}
	var kept []Job
	lastOK := ""
	for _, job := range jobs {
		parent := ""
		if br, ok := st.Branches[job.Branch]; ok {
			parent = br.Commit
		} else {
			for _, other := range st.Branches {
				if ancestor(st.ProjectPath, other.UserHead, job.UserHead) {
					parent = other.Commit
					break
				}
			}
		}
		tree := e.treeWithRecords(gitDir, key, job.Tree)
		sha, err := e.Git.commitTree(gitDir, tree, parent, "collect "+job.Branch+" "+strings.Join(job.TurnIDs, ","))
		if err != nil {
			appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, Reason: reasonFailed, Detail: err.Error()})
			e.reportCommit(job.Branch, "", err)
			kept = append(kept, job)
			continue
		}
		if _, err := e.Git.run(gitDir, nil, "update-ref", "refs/heads/"+job.Branch, sha); err != nil {
			appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, Reason: reasonFailed, Detail: err.Error()})
			e.reportCommit(job.Branch, "", err)
			kept = append(kept, job)
			continue
		}
		br := st.Branches[job.Branch]
		br.Commit = sha
		br.UserHead = job.UserHead
		br.Unpushed++
		st.Branches[job.Branch] = br
		lastOK = job.Branch
		e.reportCommit(job.Branch, sha, nil)
		e.record(key, job.SessionID, strings.Join(job.TurnIDs, ","), reasonCommit, fmt.Sprintf("%s %s 待推送 %d", job.Branch, shortSHA(sha), br.Unpushed))
	}
	warnBranch := ""
	if warnNow && lastOK != "" {
		warnBranch = e.writeWarnFile(key, gitDir, &st, lastOK, size, cfg.RepoLimitBytes)
	}
	_ = saveJobs(e.queuePath(key), kept)
	_ = st.save(e.statePath(key))
	e.maybePush(key, gitDir, &st, cfg, warnBranch)
}

func (e *Engine) treeWithRecords(gitDir, key, tree string) string {
	b, err := os.ReadFile(filepath.Join(e.DataDir, "repos", key, "records.jsonl"))
	if err != nil || len(bytes.TrimSpace(b)) == 0 || tree == "" {
		return tree
	}
	next, err := e.Git.treeWithFile(gitDir, tree, filepath.Join(e.DataDir, "index", key+"-records"), ".codex-collect/records.jsonl", b)
	if err != nil {
		return tree
	}
	return next
}

func warnFileBody(size, limit int64) []byte {
	pct := 0.0
	if limit > 0 {
		pct = float64(size) / float64(limit) * 100
	}
	return []byte(fmt.Sprintf("当前存储百分比：%.1f%%\n", pct))
}

// writeWarnFile commits git仓库存储90%.md once per crossing and returns the branch to push now.
func (e *Engine) writeWarnFile(key, gitDir string, st *repoState, prefer string, size, limit int64) string {
	branch := prefer
	if _, ok := st.Branches[branch]; !ok || st.Branches[branch].Commit == "" {
		branch = ""
		for name, br := range st.Branches {
			if br.Commit != "" {
				branch = name
				break
			}
		}
	}
	if branch == "" {
		return ""
	}
	parent := st.Branches[branch].Commit
	tree, err := e.Git.revParse(gitDir, parent+"^{tree}")
	if err != nil {
		appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, Reason: reasonFailed, Detail: err.Error()})
		e.reportCommit(branch, "", err)
		return ""
	}
	newTree, err := e.Git.treeWithFile(gitDir, tree, filepath.Join(e.DataDir, "index", key+"-warn"), warnFileName, warnFileBody(size, limit))
	if err != nil {
		appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, Reason: reasonFailed, Detail: err.Error()})
		e.reportCommit(branch, "", err)
		return ""
	}
	newTree = e.treeWithRecords(gitDir, key, newTree)
	sha, err := e.Git.commitTree(gitDir, newTree, parent, "容量达到上限的 90%")
	if err != nil {
		appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, Reason: reasonFailed, Detail: err.Error()})
		e.reportCommit(branch, "", err)
		return ""
	}
	if _, err := e.Git.run(gitDir, nil, "update-ref", "refs/heads/"+branch, sha); err != nil {
		appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, Reason: reasonFailed, Detail: err.Error()})
		e.reportCommit(branch, "", err)
		return ""
	}
	br := st.Branches[branch]
	br.Commit = sha
	br.Unpushed++
	st.Branches[branch] = br
	e.reportCommit(branch, sha, nil)
	e.record(key, "", "", reasonCommit, fmt.Sprintf("%s %s 待推送 %d 容量预警", branch, shortSHA(sha), br.Unpushed))
	return branch
}

func (e *Engine) maybePush(key, gitDir string, st *repoState, cfg Config, forceBranch string) {
	if cfg.EnterpriseNamespace == "" {
		e.noteNotPushed(key, st, 0, false)
		return
	}
	if err := e.ensureEnterpriseRepo(cfg, gitDir, key); err != nil {
		appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, Reason: reasonFailed, Detail: err.Error()})
		e.reportPush("", err)
		return
	}
	if forceBranch != "" {
		if br, ok := st.Branches[forceBranch]; ok && br.Unpushed > 0 {
			st.Pushing = true
			_ = st.save(e.statePath(key))
			go e.push(key, gitDir, forceBranch, br.Unpushed)
			return
		}
	}
	for name, br := range st.Branches {
		if br.Unpushed < cfg.PushEveryN {
			continue
		}
		if !st.NextPushAt.IsZero() && e.now().Before(st.NextPushAt) {
			e.record(key, "", "", reasonHold, fmt.Sprintf("%s 已有 %d 次，%s 后再推", name, br.Unpushed, st.NextPushAt.Format(time.RFC3339)))
			return
		}
		st.Pushing = true
		_ = st.save(e.statePath(key))
		go e.push(key, gitDir, name, br.Unpushed)
		return
	}
	e.noteNotPushed(key, st, cfg.PushEveryN, true)
}

func (e *Engine) noteNotPushed(key string, st *repoState, every int, remote bool) {
	names := make([]string, 0, len(st.Branches))
	for name, br := range st.Branches {
		if br.Unpushed > 0 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		br := st.Branches[name]
		detail := fmt.Sprintf("%s 已有 %d 次", name, br.Unpushed)
		if !remote {
			detail += "，未配置远程仓库"
		} else if every > 0 {
			detail += fmt.Sprintf("，满 %d 次再推", every)
		}
		e.record(key, "", "", reasonHold, detail)
	}
}

func (e *Engine) push(key, gitDir, branch string, count int) {
	remote, _ := e.Git.run(gitDir, []string{"GIT_TERMINAL_PROMPT=0"}, "remote", "get-url", "origin")
	args := append(gitAuthArgs(remote), "push", "origin", branch)
	_, err := e.Git.run(gitDir, []string{"GIT_TERMINAL_PROMPT=0"}, args...)
	lk := e.lock(key)
	lk.Lock()
	st := loadState(e.statePath(key))
	st.Pushing = false
	if err != nil {
		st.PushFails++
		st.NextPushAt = e.now().Add(backoff(st.PushFails))
		appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, Reason: reasonFailed, Detail: "push 失败: " + err.Error()})
	} else {
		br := st.Branches[branch]
		if br.Unpushed >= count {
			br.Unpushed -= count
		} else {
			br.Unpushed = 0
		}
		st.Branches[branch] = br
		st.PushFails = 0
		st.NextPushAt = time.Time{}
		detail := fmt.Sprintf("%s %d 个提交", branch, count)
		if remote = strings.TrimSpace(remote); remote != "" {
			detail += " " + remote
		}
		e.record(key, "", "", reasonPush, detail)
	}
	_ = st.save(e.statePath(key))
	lk.Unlock()
	e.reportPush(branch, err)
	if e.AfterPush != nil {
		e.AfterPush()
	}
}

func (e *Engine) record(key, session, turn, reason, detail string) {
	appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, SessionID: session, TurnID: turn, Reason: reason, Detail: detail})
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func backoff(fails int) time.Duration {
	switch {
	case fails <= 1:
		return 30 * time.Second
	case fails == 2:
		return 2 * time.Minute
	default:
		return 10 * time.Minute
	}
}
