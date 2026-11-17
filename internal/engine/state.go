package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	reasonFailed  = "存失败"
	reasonSkipped = "超限没打"
	reasonIdle    = "没触发"
	reasonWarn    = "容量预警"

	defaultPushEveryN   = 10
	defaultMaxFiles     = 20000
	defaultMaxFileBytes = 1 << 30
	defaultRepoLimit    = 20 << 30
	defaultWarnRatio    = 0.9
)

type Config struct {
	Email               string  `json:"email"`
	EnterpriseNamespace string  `json:"enterpriseNamespace"`
	PushEveryN          int     `json:"pushEveryN"`
	Consent             bool    `json:"consent"`
	MaxSnapshotFiles    int     `json:"maxSnapshotFiles"`
	MaxFileBytes        int64   `json:"maxFileBytes"`
	RepoLimitBytes      int64   `json:"repoLimitBytes"`
	RepoWarnRatio       float64 `json:"repoWarnRatio"`
	DataDir             string  `json:"dataDir"`
}

// EffectiveDataDir is the plugin data directory. An absolute dataDir in config
// moves repos, state, queue and records off the default folder. The config file
// itself stays in bootstrap so it can still be found.
func EffectiveDataDir(bootstrap string, cfg Config) string {
	d := strings.TrimSpace(cfg.DataDir)
	if d == "" || !filepath.IsAbs(d) {
		return bootstrap
	}
	return filepath.Clean(d)
}

func (c Config) normalized() Config {
	if c.PushEveryN <= 0 {
		c.PushEveryN = defaultPushEveryN
	}
	if c.MaxSnapshotFiles <= 0 {
		c.MaxSnapshotFiles = defaultMaxFiles
	}
	if c.MaxFileBytes <= 0 {
		c.MaxFileBytes = defaultMaxFileBytes
	}
	if c.RepoLimitBytes <= 0 {
		c.RepoLimitBytes = defaultRepoLimit
	}
	if c.RepoWarnRatio <= 0 || c.RepoWarnRatio > 1 {
		c.RepoWarnRatio = defaultWarnRatio
	}
	return c
}

type fileState struct {
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"`
	Blob  string `json:"blob"`
	Mode  string `json:"mode"`
	Kind  string `json:"kind"`
}

type branchState struct {
	Commit   string `json:"commit"`
	UserHead string `json:"userHead"`
	Unpushed int    `json:"unpushed"`
}

type repoState struct {
	RepoKey       string                 `json:"repoKey"`
	ProjectPath   string                 `json:"projectPath"`
	UserID        string                 `json:"userId"`
	UserIDSource  string                 `json:"userIdSource"`
	Files         map[string]fileState   `json:"files"`
	TranscriptLen int64                  `json:"transcriptLen"`
	Branches      map[string]branchState `json:"branches"`
	Paused        bool                   `json:"paused"`
	Warned        bool                   `json:"warned"`
	Pushing       bool                   `json:"pushing"`
	NextPushAt    time.Time              `json:"nextPushAt"`
	PushFails     int                    `json:"pushFails"`
}

type Job struct {
	Tree      string   `json:"tree"`
	Branch    string   `json:"branch"`
	TurnIDs   []string `json:"turnIds"`
	SessionID string   `json:"sessionId"`
	UserHead  string   `json:"userHead"`
}

type Record struct {
	Time      string `json:"time"`
	RepoKey   string `json:"repoKey"`
	SessionID string `json:"sessionId,omitempty"`
	TurnID    string `json:"turnId,omitempty"`
	Reason    string `json:"reason"`
	Detail    string `json:"detail,omitempty"`
	Bytes     int64  `json:"bytes,omitempty"`
}

type Result struct {
	Action string `json:"action"`
	Detail string `json:"detail,omitempty"`
}

func canonical(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	abs = filepath.Clean(abs)
	abs = strings.TrimRight(abs, `\/`)
	if len(abs) >= 2 && abs[1] == ':' {
		abs = strings.ToUpper(abs[:1]) + abs[1:]
	}
	return filepath.ToSlash(abs)
}

func repoKey(userID, project string) string {
	sum := sha256.Sum256([]byte(userID + "\x00" + project))
	return hex.EncodeToString(sum[:])
}

func loadConfig(dataDir string) Config {
	b, err := os.ReadFile(filepath.Join(dataDir, "config.json"))
	if err != nil {
		return Config{}.normalized()
	}
	var c Config
	if json.Unmarshal(b, &c) != nil {
		return Config{}.normalized()
	}
	return c.normalized()
}

func (r *repoState) save(path string) error {
	if r.Files == nil {
		r.Files = map[string]fileState{}
	}
	if r.Branches == nil {
		r.Branches = map[string]branchState{}
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func loadState(path string) repoState {
	var st repoState
	b, err := os.ReadFile(path)
	if err != nil {
		st.Files = map[string]fileState{}
		st.Branches = map[string]branchState{}
		return st
	}
	_ = json.Unmarshal(b, &st)
	if st.Files == nil {
		st.Files = map[string]fileState{}
	}
	if st.Branches == nil {
		st.Branches = map[string]branchState{}
	}
	return st
}

func appendRecord(dataDir string, rec Record) {
	if rec.Time == "" {
		rec.Time = time.Now().Format(time.RFC3339)
	}
	var path string
	if rec.RepoKey != "" {
		dir := filepath.Join(dataDir, "repos", rec.RepoKey)
		_ = os.MkdirAll(dir, 0o755)
		path = filepath.Join(dir, "records.jsonl")
	} else {
		dir := filepath.Join(dataDir, "records")
		_ = os.MkdirAll(dir, 0o755)
		path = filepath.Join(dir, "unknown.jsonl")
	}
	b, _ := json.Marshal(rec)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

func parseCountObjects(out string) int64 {
	var kib int64
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "size:") || strings.HasPrefix(line, "size-pack:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				n, err := strconv.ParseInt(fields[1], 10, 64)
				if err == nil {
					kib += n
				}
			}
		}
	}
	return kib * 1024
}

func remoteURL(namespace, key string) string {
	return strings.TrimRight(namespace, "/") + "/" + key + ".git"
}

func (e *Engine) ensureEnterpriseRepo(cfg Config, gitDir, key string) error {
	url := remoteURL(cfg.EnterpriseNamespace, key)
	if err := ensureRemoteProject(cfg.EnterpriseNamespace, key); err != nil {
		return err
	}
	return e.ensureOrigin(gitDir, url)
}

// ensureRemoteLater creates the remote without blocking the hook.
// The host request stays outside the repo lock so a slow response cannot hold the next turn.
func (e *Engine) ensureRemoteLater(cfg Config, gitDir, key string) {
	url := remoteURL(cfg.EnterpriseNamespace, key)
	if err := ensureRemoteProject(cfg.EnterpriseNamespace, key); err != nil {
		appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, Reason: reasonFailed, Detail: err.Error()})
		return
	}
	lk := e.lock(key)
	lk.Lock()
	defer lk.Unlock()
	if err := e.ensureOrigin(gitDir, url); err != nil {
		appendRecord(e.DataDir, Record{Time: e.now().Format(time.RFC3339), RepoKey: key, Reason: reasonFailed, Detail: err.Error()})
	}
}

func (e *Engine) ensureOrigin(gitDir, url string) error {
	if _, err := e.Git.run(gitDir, nil, "remote", "get-url", "origin"); err == nil {
		_, err = e.Git.run(gitDir, nil, "remote", "set-url", "origin", url)
		return err
	}
	_, err := e.Git.run(gitDir, nil, "remote", "add", "origin", url)
	return err
}

func relSlash(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func indexInfo(mode, sha, path string) []byte {
	var b bytes.Buffer
	b.WriteString(mode)
	b.WriteByte(' ')
	b.WriteString(sha)
	b.WriteString(" 0\t")
	b.WriteString(path)
	b.WriteByte(0)
	return b.Bytes()
}
