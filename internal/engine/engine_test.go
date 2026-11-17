package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSealUsesProjectPathWithoutCopy(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	data := t.TempDir()
	must(t, os.WriteFile(filepath.Join(data, "config.json"), []byte(`{"email":"u42@example.com","consent":true,"pushEveryN":10}`), 0o644))
	must(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "src", "app.py"), []byte("v1"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, ".env"), []byte("TOKEN=secret"), 0o644))
	must(t, os.MkdirAll(filepath.Join(root, "models"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "models", "w.pt"), []byte("MODEL-WEIGHTS-DO-NOT-COMMIT"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "models", "readme.md"), []byte("notes"), 0o644))
	gitInitUser(t, root)

	eng := &Engine{DataDir: data}
	ev := event(root, "turn-1")
	res := eng.Handle(ev)
	if res.Action != "sealed" {
		t.Fatalf("seal: %+v records=%s", res, readRecords(t, data))
	}
	eng.CommitAll(Config{Consent: true, PushEveryN: 10})

	gitDir := collectionGit(t, data)
	if got := gitShow(t, gitDir, "HEAD:src/app.py"); got != "v1" {
		t.Fatalf("snapshot = %q", got)
	}
	if got := gitShow(t, gitDir, "HEAD:.env"); strings.Contains(got, "TOKEN=secret") || !strings.Contains(got, "因密钥排除") {
		t.Fatalf("secret leaked: %q", got)
	}
	manifest := gitShow(t, gitDir, "HEAD:.codex-collect/manifest.json")
	if !strings.Contains(manifest, "因密钥排除") || !strings.Contains(manifest, "models/w.pt") {
		t.Fatalf("manifest: %s", manifest)
	}
	if cmd := exec.Command("git", "--git-dir", gitDir, "cat-file", "-e", "HEAD:models/w.pt"); cmd.Run() == nil {
		t.Fatal("model file entered the commit")
	}
	if got := gitShow(t, gitDir, "HEAD:models/readme.md"); got != "notes" {
		t.Fatalf("text file in models was skipped: %q", got)
	}
	repoDir := filepath.Dir(gitDir)
	diskManifest, err := os.ReadFile(filepath.Join(repoDir, "manifest.json"))
	must(t, err)
	if !strings.Contains(string(diskManifest), "models/w.pt") {
		t.Fatalf("repo manifest: %s", diskManifest)
	}
	if _, err := os.Stat(filepath.Join(root, ".codex-collect")); !os.IsNotExist(err) {
		t.Fatal("collector wrote into the user project")
	}
	if _, err := os.Stat(filepath.Join(data, "staging")); !os.IsNotExist(err) {
		t.Fatal("project files were copied to staging")
	}

	must(t, os.WriteFile(filepath.Join(root, "src", "app.py"), []byte("v2"), 0o644))
	res = eng.Handle(event(root, "turn-2"))
	if res.Action != "sealed" {
		t.Fatalf("second seal: %+v", res)
	}
	if got := gitShow(t, gitDir, "HEAD:src/app.py"); got != "v1" {
		t.Fatalf("commit ran before the hook returned, got %q", got)
	}
	eng.CommitAll(Config{Consent: true, PushEveryN: 10})
	if got := gitShow(t, gitDir, "HEAD~1:src/app.py"); got != "v1" {
		t.Fatalf("older snapshot = %q", got)
	}
	if got := gitShow(t, gitDir, "HEAD:src/app.py"); got != "v2" {
		t.Fatalf("newer snapshot = %q", got)
	}
	if got := gitShow(t, gitDir, "HEAD:.env"); strings.Contains(got, "TOKEN=secret") || !strings.Contains(got, "因密钥排除") {
		t.Fatalf("unchanged secret was reread wrong: %q", got)
	}

	res = eng.Handle(event(root, "turn-3"))
	if res.Action != "skip" {
		t.Fatalf("unchanged round should skip, got %+v", res)
	}
}

func TestHelloSkipsWhenFilesStayTheSame(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	data := t.TempDir()
	must(t, os.WriteFile(filepath.Join(data, "config.json"), []byte(`{"email":"u42@example.com","consent":true,"pushEveryN":10}`), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "hello.py"), []byte("print('hi')\n"), 0o644))

	eng := &Engine{DataDir: data}
	start := eng.Handle([]byte(`{"hook_event_name":"SessionStart","session_id":"sess","cwd":"` + filepath.ToSlash(root) + `"}`))
	if start.Action != "ready" {
		t.Fatalf("start: %+v", start)
	}
	res := eng.Handle(event(root, "turn-1"))
	if res.Action != "skip" {
		t.Fatalf("hello should skip, got %+v", res)
	}
	eng.CommitAll(Config{Consent: true, PushEveryN: 10})
	gitDir := collectionGit(t, data)
	if _, err := exec.Command("git", "--git-dir", gitDir, "rev-parse", "HEAD").CombinedOutput(); err == nil {
		t.Fatal("hello created a commit")
	}

	must(t, os.WriteFile(filepath.Join(root, "hello.py"), []byte("print('bye')\n"), 0o644))
	res = eng.Handle(event(root, "turn-2"))
	if res.Action != "sealed" {
		t.Fatalf("edit should seal, got %+v", res)
	}
}

func TestLargePathUsesSuffixNotDirectory(t *testing.T) {
	if isLargePath("models/readme.md", 20, 1<<30) {
		t.Fatal("plain file in models was treated as large")
	}
	if !isLargePath("models/w.pt", 20, 1<<30) {
		t.Fatal("weight suffix should be large")
	}
	if !isLargePath("src/data.zip", 20, 1<<30) {
		t.Fatal("archive suffix should be large")
	}
	if isLargePath("src/app.py", 1<<30, 1<<30) {
		t.Fatal("1GB file should stay under the limit")
	}
	if !isLargePath("src/app.py", 1<<30+1, 1<<30) {
		t.Fatal("file over 1GB should be large")
	}
}

func TestTrajectoryAndRecordsLiveUnderRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	data := t.TempDir()
	must(t, os.WriteFile(filepath.Join(data, "config.json"), []byte(`{"email":"u42@example.com","consent":true,"pushEveryN":10}`), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "hello.py"), []byte("v1"), 0o644))
	transcript := filepath.Join(t.TempDir(), "rollout.jsonl")
	must(t, os.WriteFile(transcript, []byte("user: hello\n"), 0o644))

	eng := &Engine{DataDir: data}
	raw := `{"hook_event_name":"Stop","session_id":"sess","turn_id":"turn-1","cwd":"` + filepath.ToSlash(root) + `","transcript_path":"` + filepath.ToSlash(transcript) + `","model":"test"}`
	res := eng.Handle([]byte(raw))
	if res.Action != "sealed" {
		t.Fatalf("seal: %+v %s", res, readRecords(t, data))
	}
	gitDir := collectionGit(t, data)
	key := filepath.Base(filepath.Dir(gitDir))
	appendRecord(data, Record{RepoKey: key, Reason: reasonIdle, Detail: "测试记录"})
	eng.CommitAll(Config{Consent: true, PushEveryN: 10})

	traj := filepath.Join(filepath.Dir(gitDir), "trajectory", "rollout.jsonl")
	body, err := os.ReadFile(traj)
	must(t, err)
	if string(body) != "user: hello\n" {
		t.Fatalf("trajectory file: %q", body)
	}
	branch := headBranch(t, gitDir)
	if got := gitShow(t, gitDir, branch+":.codex-collect/trajectory/rollout.jsonl"); got != "user: hello\n" {
		t.Fatalf("committed trajectory: %q", got)
	}
	if got := gitShow(t, gitDir, branch+":.codex-collect/records.jsonl"); !strings.Contains(got, "测试记录") {
		t.Fatalf("committed records: %q", got)
	}
}

func TestFindRolloutAcrossVersions(t *testing.T) {
	home := t.TempDir()
	dated := filepath.Join(home, ".codex", "sessions", "2026", "11", "17", "rollout-2026-11-17T01-02-03-parentid.jsonl")
	child := filepath.Join(filepath.Dir(dated), "rollout-2026-11-17T01-02-03-parentid_childid.jsonl")
	flat := filepath.Join(home, ".codex", "archived_sessions", "rollout-2026-04-14T01-02-03-flatid.jsonl")
	must(t, os.MkdirAll(filepath.Dir(dated), 0o755))
	must(t, os.MkdirAll(filepath.Dir(flat), 0o755))
	must(t, os.WriteFile(dated, []byte("parent\n"), 0o644))
	must(t, os.WriteFile(dated+".zst", []byte("old-compressed"), 0o644))
	must(t, os.WriteFile(child, []byte("child\n"), 0o644))
	must(t, os.WriteFile(flat, []byte("flat\n"), 0o644))

	eng := &Engine{Home: home}
	if got := eng.findRollout("parentid", ""); got != dated {
		t.Fatalf("dated rollout = %s", got)
	}
	if got := eng.findRollout("flatid", ""); got != flat {
		t.Fatalf("flat archived rollout = %s", got)
	}
	files := eng.collectTrajectories(hookEvent{SessionID: "parentid"}, "repo")
	names := map[string]string{}
	for _, file := range files {
		names[file.Name] = string(file.Body)
	}
	if names["rollout-2026-11-17T01-02-03-parentid.jsonl"] != "parent\n" {
		t.Fatalf("parent file: %#v", names)
	}
	if names["rollout-2026-11-17T01-02-03-parentid_childid.jsonl"] != "child\n" {
		t.Fatalf("child file: %#v", names)
	}
}

func TestEffectiveDataDir(t *testing.T) {
	base := filepath.Join(t.TempDir(), ".codex-collector")
	if got := EffectiveDataDir(base, Config{}); got != base {
		t.Fatalf("default = %s", got)
	}
	if got := EffectiveDataDir(base, Config{DataDir: "relative"}); got != base {
		t.Fatalf("relative should stay default, got %s", got)
	}
	custom := filepath.Join(t.TempDir(), "data")
	if got := EffectiveDataDir(base, Config{DataDir: custom}); got != filepath.Clean(custom) {
		t.Fatalf("custom = %s", got)
	}
}

func TestWarnFileWhenRepoCrossesNinetyPercent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	data := t.TempDir()
	must(t, os.WriteFile(filepath.Join(data, "config.json"), []byte(`{"email":"u42@example.com","consent":true,"pushEveryN":10}`), 0o644))
	payload := make([]byte, 256<<10)
	for i := range payload {
		payload[i] = byte(i)
	}
	must(t, os.WriteFile(filepath.Join(root, "hello.py"), payload, 0o644))

	eng := &Engine{DataDir: data}
	res := eng.Handle(event(root, "turn-1"))
	if res.Action != "sealed" {
		t.Fatalf("seal: %+v %s", res, readRecords(t, data))
	}
	eng.CommitAll(Config{Consent: true, PushEveryN: 10, RepoLimitBytes: 1 << 40})
	gitDir := collectionGit(t, data)

	must(t, os.WriteFile(filepath.Join(root, "hello.py"), []byte("changed\n"), 0o644))
	res = eng.Handle(event(root, "turn-2"))
	if res.Action != "sealed" {
		t.Fatalf("second seal: %+v", res)
	}
	limit := limitAtNinety(t, eng, gitDir)
	eng.CommitAll(Config{Consent: true, PushEveryN: 10, RepoLimitBytes: limit, RepoWarnRatio: 0.9})
	branch := headBranch(t, gitDir)
	body := gitShow(t, gitDir, branch+":"+warnFileName)
	if !strings.Contains(body, "%") {
		t.Fatalf("warn file: %q", body)
	}
	if got := strings.TrimSpace(gitShow(t, gitDir, branch+":hello.py")); got != "changed" {
		t.Fatalf("warn commit dropped project files: len=%d", len(got))
	}
	if !strings.Contains(readRecords(t, data), "容量预警") {
		t.Fatal("missing warn record")
	}
	before := commitCount(t, gitDir, branch)

	must(t, os.WriteFile(filepath.Join(root, "hello.py"), []byte("again\n"), 0o644))
	res = eng.Handle(event(root, "turn-3"))
	if res.Action != "sealed" {
		t.Fatalf("third seal: %+v", res)
	}
	limit = limitAtNinety(t, eng, gitDir)
	eng.CommitAll(Config{Consent: true, PushEveryN: 10, RepoLimitBytes: limit, RepoWarnRatio: 0.9})
	if got := commitCount(t, gitDir, branch); got != before+1 {
		t.Fatalf("warn file committed again: commits %d -> %d", before, got)
	}
}

func limitAtNinety(t *testing.T, eng *Engine, gitDir string) int64 {
	t.Helper()
	size, err := eng.Git.countBytes(gitDir)
	must(t, err)
	if size == 0 {
		t.Fatal("repo size is 0")
	}
	limit := size * 10 / 9
	if limit <= size {
		limit = size + 1
	}
	for limit > size && int64(float64(limit)*0.9) > size {
		limit--
	}
	if size >= limit {
		t.Fatalf("cannot place size %d under limit %d", size, limit)
	}
	return limit
}

func headBranch(t *testing.T, gitDir string) string {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", gitDir, "for-each-ref", "--format=%(refname:short)", "refs/heads").CombinedOutput()
	if err != nil {
		t.Fatalf("branches: %v %s", err, out)
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" || strings.Contains(branch, "\n") {
		t.Fatalf("expected one branch, got %q", branch)
	}
	return branch
}

func commitCount(t *testing.T, gitDir, branch string) int {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", gitDir, "rev-list", "--count", branch).CombinedOutput()
	if err != nil {
		t.Fatalf("rev-list: %v %s", err, out)
	}
	n := 0
	if _, err := fmt.Sscan(strings.TrimSpace(string(out)), &n); err != nil {
		t.Fatal(err)
	}
	return n
}

func event(root, turn string) []byte {
	return []byte(`{"hook_event_name":"Stop","session_id":"sess","turn_id":"` + turn + `","cwd":"` + filepath.ToSlash(root) + `","model":"test"}`)
}

func collectionGit(t *testing.T, data string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(data, "repos", "*", ".git"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("git dir: %v %v", matches, err)
	}
	return matches[0]
}

func gitShow(t *testing.T, gitDir, spec string) string {
	t.Helper()
	cmd := exec.Command("git", "--git-dir", gitDir, "show", spec)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git show %s: %v %s", spec, err, out)
	}
	return string(out)
}

func gitInitUser(t *testing.T, root string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=user", "GIT_AUTHOR_EMAIL=user@local", "GIT_COMMITTER_NAME=user", "GIT_COMMITTER_EMAIL=user@local")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v %s", args, err, out)
		}
	}
	run("init")
	run("add", "src/app.py")
	run("-c", "user.name=user", "-c", "user.email=user@local", "commit", "-m", "user")
}

func readRecords(t *testing.T, data string) string {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(data, "records", "*.jsonl"))
	repoRecords, _ := filepath.Glob(filepath.Join(data, "repos", "*", "records.jsonl"))
	matches = append(matches, repoRecords...)
	var b strings.Builder
	for _, m := range matches {
		raw, _ := os.ReadFile(m)
		b.Write(raw)
	}
	return b.String()
}

func TestClearStalePush(t *testing.T) {
	data := t.TempDir()
	eng := &Engine{DataDir: data}
	stuck := repoState{Pushing: true, Branches: map[string]branchState{"nogit": {Unpushed: 1}}}
	idle := repoState{Branches: map[string]branchState{"nogit": {Unpushed: 2}}}
	must(t, stuck.save(eng.statePath("stuck")))
	must(t, idle.save(eng.statePath("idle")))
	eng.ClearStalePush()
	if loadState(eng.statePath("stuck")).Pushing {
		t.Fatal("stale pushing flag was kept")
	}
	if got := loadState(eng.statePath("idle")).Branches["nogit"].Unpushed; got != 2 {
		t.Fatalf("idle unpushed = %d", got)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
