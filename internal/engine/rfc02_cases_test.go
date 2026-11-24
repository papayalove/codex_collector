package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRFC02Cases(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Run("T1", testNoFileChange)
	t.Run("T2", testCommitAfterStop)
	t.Run("T3", testSessionEndTrajectory)
	t.Run("T4", testScopeIsGitRoot)
	t.Run("T5", testNoGitBranch)
	t.Run("T6", testHomeSkipped)
	t.Run("T7", testTooManyFiles)
	t.Run("T8-T16-T18-T20", testSpecialFiles)
	t.Run("T17", testSizeFallback)
}

func testNoFileChange(t *testing.T) {
	root, data, eng := newCase(t, "")
	must(t, os.WriteFile(filepath.Join(root, "hello.py"), []byte("print('hi')\n"), 0o644))
	if res := eng.Handle(startEvent(root)); res.Action != "ready" {
		t.Fatalf("start %+v", res)
	}
	if res := eng.Handle(event(root, "turn-1")); res.Action != "skip" {
		t.Fatalf("stop %+v", res)
	}
	eng.CommitAll(Config{Consent: true, PushEveryN: 10})
	gitDir := collectionGit(t, data)
	out, err := exec.Command("git", "--git-dir", gitDir, "for-each-ref", "refs/heads").CombinedOutput()
	if err != nil {
		t.Fatalf("refs: %v %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("unchanged round created a commit: %s", out)
	}
	recs := readRecords(t, data)
	if !strings.Contains(recs, "没触发") || !strings.Contains(recs, "没有文件改动") {
		t.Fatalf("records: %s", recs)
	}
}

func testCommitAfterStop(t *testing.T) {
	root, data, eng := newCase(t, "")
	path := filepath.Join(root, "app.txt")
	must(t, os.WriteFile(path, []byte("partial"), 0o644))
	must(t, os.WriteFile(path, []byte("final-content"), 0o644))
	if res := eng.Handle(event(root, "turn-1")); res.Action != "sealed" {
		t.Fatalf("stop %+v %s", res, readRecords(t, data))
	}
	gitDir := collectionGit(t, data)
	if branchExists(gitDir, "nogit") {
		t.Fatal("commit happened before the hook returned")
	}
	eng.CommitAll(Config{Consent: true, PushEveryN: 10})
	if got := gitShow(t, gitDir, "nogit:app.txt"); got != "final-content" {
		t.Fatalf("snapshot = %q", got)
	}
}

func testSessionEndTrajectory(t *testing.T) {
	root, data, eng := newCase(t, "")
	must(t, os.WriteFile(filepath.Join(root, "app.txt"), []byte("v1"), 0o644))
	transcript := filepath.Join(t.TempDir(), "rollout.jsonl")
	must(t, os.WriteFile(transcript, []byte("user: one\n"), 0o644))
	if res := eng.Handle(stopWithTranscript(root, "turn-1", transcript)); res.Action != "sealed" {
		t.Fatalf("stop %+v %s", res, readRecords(t, data))
	}
	eng.CommitAll(Config{Consent: true, PushEveryN: 10})
	gitDir := collectionGit(t, data)
	if commitCount(t, gitDir, "nogit") != 1 {
		t.Fatalf("commits after edit = %d", commitCount(t, gitDir, "nogit"))
	}
	if res := eng.Handle(sessionEnd(root, transcript)); res.Action != "skip" {
		t.Fatalf("unchanged session end %+v", res)
	}
	must(t, os.WriteFile(transcript, []byte("user: one\nuser: two\n"), 0o644))
	if res := eng.Handle(sessionEnd(root, transcript)); res.Action != "sealed" {
		t.Fatalf("session end %+v %s", res, readRecords(t, data))
	}
	eng.CommitAll(Config{Consent: true, PushEveryN: 10})
	if commitCount(t, gitDir, "nogit") != 2 {
		t.Fatalf("commits after session end = %d", commitCount(t, gitDir, "nogit"))
	}
	subject := gitLogSubject(t, gitDir, "nogit")
	if !strings.Contains(subject, "session-end") {
		t.Fatalf("subject %q", subject)
	}
}

func testScopeIsGitRoot(t *testing.T) {
	root, data, eng := newCase(t, "")
	must(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "root.txt"), []byte("root"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "sub", "a.txt"), []byte("a"), 0o644))
	gitInitAll(t, root)
	before := userRev(t, root)
	must(t, os.WriteFile(filepath.Join(root, "sub", "a.txt"), []byte("a2"), 0o644))
	if res := eng.Handle(event(filepath.Join(root, "sub"), "turn-1")); res.Action != "sealed" {
		t.Fatalf("stop %+v %s", res, readRecords(t, data))
	}
	eng.CommitAll(Config{Consent: true, PushEveryN: 10})
	gitDir := collectionGit(t, data)
	branch := headBranch(t, gitDir)
	if got := gitShow(t, gitDir, branch+":root.txt"); got != "root" {
		t.Fatalf("root file %q", got)
	}
	if got := gitShow(t, gitDir, branch+":sub/a.txt"); got != "a2" {
		t.Fatalf("sub file %q", got)
	}
	if after := userRev(t, root); after != before {
		t.Fatalf("user git changed %s -> %s", before, after)
	}
}

func testNoGitBranch(t *testing.T) {
	root, data, eng := newCase(t, "")
	must(t, os.WriteFile(filepath.Join(root, "only.txt"), []byte("x"), 0o644))
	if res := eng.Handle(event(root, "turn-1")); res.Action != "sealed" {
		t.Fatalf("stop %+v %s", res, readRecords(t, data))
	}
	eng.CommitAll(Config{Consent: true, PushEveryN: 10})
	if headBranch(t, collectionGit(t, data)) != "nogit" {
		t.Fatalf("branch %s", headBranch(t, collectionGit(t, data)))
	}
}

func testHomeSkipped(t *testing.T) {
	home, err := os.UserHomeDir()
	must(t, err)
	data := t.TempDir()
	writeCaseConfig(t, data, "")
	eng := &Engine{DataDir: data}
	res := eng.Handle(startEvent(home))
	if res.Detail != "家目录" {
		t.Fatalf("start %+v", res)
	}
	recs := readRecords(t, data)
	if !strings.Contains(recs, "超限没打") || !strings.Contains(recs, "家目录") {
		t.Fatalf("records: %s", recs)
	}
	matches, _ := filepath.Glob(filepath.Join(data, "repos", "*", ".git"))
	if len(matches) != 0 {
		t.Fatalf("home created a repo: %v", matches)
	}
}

func testTooManyFiles(t *testing.T) {
	root, data, eng := newCase(t, `"maxSnapshotFiles":3`)
	for _, name := range []string{"a.txt", "b.txt", "c.txt", "d.txt"} {
		must(t, os.WriteFile(filepath.Join(root, name), []byte(name), 0o644))
	}
	if res := eng.Handle(event(root, "turn-1")); res.Detail != "文件数超过 2 万" {
		t.Fatalf("stop %+v %s", res, readRecords(t, data))
	}
	recs := readRecords(t, data)
	if !strings.Contains(recs, "超限没打") || !strings.Contains(recs, "文件数超过 2 万") {
		t.Fatalf("records: %s", recs)
	}
	gitDir := collectionGit(t, data)
	if branchExists(gitDir, "nogit") {
		t.Fatal("over-limit snapshot was committed")
	}
}

func testSpecialFiles(t *testing.T) {
	root, data, eng := newCase(t, "")
	must(t, os.MkdirAll(filepath.Join(root, "models"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, "empty"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, "nested"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "models", "readme.md"), []byte("notes"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "models", "w.pt"), []byte("WEIGHT"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "pack.zip"), []byte("zip"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "data.parquet"), []byte("parquet"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "model.safetensors"), []byte("safe"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "nested", "readme.md"), []byte("nested"), 0o644))
	gitInitAll(t, filepath.Join(root, "nested"))
	must(t, os.WriteFile(filepath.Join(root, "crlf.txt"), []byte("a\r\nb\r\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "说明 文档.txt"), []byte("中文"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored.txt\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "ignored.txt"), []byte("ignored-body"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "run.sh"), []byte("#!/bin/sh\necho hi\n"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, ".env"), []byte("TOKEN=secret-env"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "a.pem"), []byte("pem-secret"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "id_rsa"), []byte("rsa-secret"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "credential.json"), []byte(`{"password":"x"}`), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "secret.txt"), []byte("key AKIAIOSFODNN7EXAMPLE\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "target.txt"), []byte("target-body"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "plain.txt"), []byte("v1"), 0o644))
	gitInitAll(t, root)
	userBefore := userRev(t, root)
	linkOK := os.Symlink("target.txt", filepath.Join(root, "link.txt")) == nil

	if res := eng.Handle(event(root, "turn-1")); res.Action != "sealed" {
		t.Fatalf("round1 %+v %s", res, readRecords(t, data))
	}
	eng.CommitAll(Config{Consent: true, PushEveryN: 10})
	gitDir := collectionGit(t, data)
	branch := headBranch(t, gitDir)
	tree := gitLs(t, gitDir, branch)

	if gitShow(t, gitDir, branch+":nested/readme.md") != "nested" {
		t.Fatal("nested worktree file missing")
	}
	if strings.Contains(tree, "nested/.git") {
		t.Fatal("nested .git entered the commit")
	}
	if linkOK {
		if !strings.Contains(tree, "120000") || !strings.Contains(tree, "link.txt") {
			t.Fatalf("symlink tree:\n%s", tree)
		}
		if got := gitShow(t, gitDir, branch+":link.txt"); strings.Contains(got, "target-body") {
			t.Fatalf("symlink stored target content: %q", got)
		}
	} else {
		t.Log("T9 skipped: this machine refused to create a symlink")
	}
	if strings.Contains(tree, "100755") {
		t.Fatal("executable bit was stored")
	}
	if !strings.Contains(tree, "100644") || !strings.Contains(tree, "run.sh") {
		t.Fatal("script missing")
	}
	if strings.Contains(tree, "empty/.keep") || strings.Contains(tree, "empty") {
		t.Fatalf("empty dir stored:\n%s", tree)
	}
	if gitShow(t, gitDir, branch+":crlf.txt") != "a\r\nb\r\n" {
		t.Fatalf("crlf changed: %q", gitShow(t, gitDir, branch+":crlf.txt"))
	}
	if gitShow(t, gitDir, branch+":说明 文档.txt") != "中文" {
		t.Fatal("unicode name was rewritten")
	}
	if gitShow(t, gitDir, branch+":ignored.txt") != "ignored-body" {
		t.Fatal("gitignored file was dropped")
	}
	if gitShow(t, gitDir, branch+":models/readme.md") != "notes" {
		t.Fatal("text under models was dropped")
	}
	for _, missing := range []string{"models/w.pt", "pack.zip", "data.parquet", "model.safetensors"} {
		if !gitAbsent(gitDir, branch+":"+missing) {
			t.Fatalf("%s entered the commit", missing)
		}
	}
	manifest := gitShow(t, gitDir, branch+":.trace-collector/codex/manifest.json")
	for _, item := range []string{"models/w.pt", "pack.zip", "data.parquet", "model.safetensors"} {
		if !strings.Contains(manifest, item) || !strings.Contains(manifest, "大文件") {
			t.Fatalf("manifest missing large %s: %s", item, manifest)
		}
	}
	host, _ := os.Hostname()
	if !strings.Contains(manifest, host+":") {
		t.Fatalf("manifest remote has no hostname: %s", manifest)
	}
	for _, secret := range []string{".env", "a.pem", "id_rsa", "credential.json", "secret.txt"} {
		got := gitShow(t, gitDir, branch+":"+secret)
		if strings.Contains(got, "TOKEN=secret-env") || strings.Contains(got, "pem-secret") || strings.Contains(got, "rsa-secret") || strings.Contains(got, "AKIA") || strings.Contains(got, `"password"`) {
			t.Fatalf("secret leaked in %s: %q", secret, got)
		}
		if !strings.Contains(got, "因密钥排除") {
			t.Fatalf("placeholder missing for %s: %q", secret, got)
		}
	}
	if !strings.Contains(manifest, "因密钥排除") {
		t.Fatalf("manifest secrets: %s", manifest)
	}

	must(t, os.WriteFile(filepath.Join(root, "plain.txt"), []byte("v2"), 0o644))
	if res := eng.Handle(event(root, "turn-2")); res.Action != "sealed" {
		t.Fatalf("round2 %+v", res)
	}
	must(t, os.WriteFile(filepath.Join(root, "plain.txt"), []byte("v3"), 0o644))
	if res := eng.Handle(event(root, "turn-3")); res.Action != "sealed" {
		t.Fatalf("round3 %+v", res)
	}
	eng.CommitAll(Config{Consent: true, PushEveryN: 10})
	if commitCount(t, gitDir, branch) != 3 {
		t.Fatalf("commits = %d", commitCount(t, gitDir, branch))
	}
	if gitShow(t, gitDir, branch+"~2:plain.txt") != "v1" || gitShow(t, gitDir, branch+"~1:plain.txt") != "v2" || gitShow(t, gitDir, branch+":plain.txt") != "v3" {
		t.Fatal("three rounds did not form a chain")
	}
	if userRev(t, root) != userBefore {
		t.Fatal("user git history changed")
	}
}

func testSizeFallback(t *testing.T) {
	root, data, eng := newCase(t, `"maxFileBytes":20`)
	must(t, os.WriteFile(filepath.Join(root, "result.txt"), []byte(strings.Repeat("x", 30)), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "note.txt"), []byte("small"), 0o644))
	if res := eng.Handle(event(root, "turn-1")); res.Action != "sealed" {
		t.Fatalf("stop %+v %s", res, readRecords(t, data))
	}
	eng.CommitAll(Config{Consent: true, PushEveryN: 10})
	gitDir := collectionGit(t, data)
	if !gitAbsent(gitDir, "nogit:result.txt") {
		t.Fatal("oversize text entered the commit")
	}
	if gitShow(t, gitDir, "nogit:note.txt") != "small" {
		t.Fatal("small file was dropped")
	}
	manifest := gitShow(t, gitDir, "nogit:.trace-collector/codex/manifest.json")
	if !strings.Contains(manifest, "result.txt") || !strings.Contains(manifest, "大文件") {
		t.Fatalf("manifest: %s", manifest)
	}
}

func newCase(t *testing.T, extra string) (string, string, *Engine) {
	t.Helper()
	root := t.TempDir()
	data := t.TempDir()
	writeCaseConfig(t, data, extra)
	return root, data, &Engine{DataDir: data}
}

func writeCaseConfig(t *testing.T, data, extra string) {
	t.Helper()
	body := `{"email":"u42@example.com","consent":true,"pushEveryN":10`
	if extra != "" {
		body += "," + extra
	}
	body += "}"
	must(t, os.WriteFile(filepath.Join(data, "config.json"), []byte(body), 0o644))
}

func startEvent(root string) []byte {
	return []byte(`{"hook_event_name":"SessionStart","session_id":"sess","cwd":"` + filepath.ToSlash(root) + `"}`)
}

func stopWithTranscript(root, turn, transcript string) []byte {
	return []byte(`{"hook_event_name":"Stop","session_id":"sess","turn_id":"` + turn + `","cwd":"` + filepath.ToSlash(root) + `","transcript_path":"` + filepath.ToSlash(transcript) + `","model":"test"}`)
}

func sessionEnd(root, transcript string) []byte {
	return []byte(`{"hook_event_name":"SessionEnd","session_id":"sess","cwd":"` + filepath.ToSlash(root) + `","transcript_path":"` + filepath.ToSlash(transcript) + `"}`)
}

func branchExists(gitDir, branch string) bool {
	return exec.Command("git", "--git-dir", gitDir, "rev-parse", "--verify", "--quiet", branch).Run() == nil
}

func gitLs(t *testing.T, gitDir, branch string) string {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", gitDir, "ls-tree", "-r", branch).CombinedOutput()
	if err != nil {
		t.Fatalf("ls-tree: %v %s", err, out)
	}
	return string(out)
}

func gitAbsent(gitDir, spec string) bool {
	return exec.Command("git", "--git-dir", gitDir, "cat-file", "-e", spec).Run() != nil
}

func gitLogSubject(t *testing.T, gitDir, branch string) string {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", gitDir, "log", "-1", "--format=%s", branch).CombinedOutput()
	if err != nil {
		t.Fatalf("log: %v %s", err, out)
	}
	return string(out)
}

func userRev(t *testing.T, root string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("user rev: %v %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func gitInitAll(t *testing.T, root string) {
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
	run("add", "-A")
	run("-c", "user.name=user", "-c", "user.email=user@local", "commit", "-m", "user")
}
