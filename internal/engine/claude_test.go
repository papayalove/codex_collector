package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostLabelDefaultsToCodex(t *testing.T) {
	if hostLabel("") != "codex" || hostLabel("  ") != "codex" {
		t.Fatal("empty host should default to codex")
	}
	if hostLabel("claude") != "claude" {
		t.Fatal("explicit host should pass through")
	}
}

// A Claude Code Stop event flows through the same pipeline as a Codex one: the
// snapshot is sealed, the transcript named by transcript_path is stored, and
// the metadata records which host produced the session.
func TestClaudeStopSealsSnapshotAndTranscript(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	data := t.TempDir()
	must(t, os.WriteFile(filepath.Join(data, "config.json"), []byte(`{"email":"u42@example.com","consent":true,"pushEveryN":10}`), 0o644))
	must(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "src", "app.py"), []byte("v1"), 0o644))
	gitInitUser(t, root)

	home := t.TempDir()
	sessDir := filepath.Join(home, ".claude", "projects", "d-projects-demo")
	must(t, os.MkdirAll(sessDir, 0o755))
	transcript := filepath.Join(sessDir, "b7e49c6d.jsonl")
	must(t, os.WriteFile(transcript, []byte("{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"hello\"}}\n"), 0o644))

	eng := &Engine{DataDir: data, Home: home}
	ev := []byte(`{"hook_event_name":"Stop","session_id":"b7e49c6d","turn_id":"claude-1a2b3c4d5e6f7081","cwd":"` + filepath.ToSlash(root) + `","transcript_path":"` + filepath.ToSlash(transcript) + `","host":"claude"}`)
	res := eng.Handle(ev)
	if res.Action != "sealed" {
		t.Fatalf("claude seal: %+v records=%s", res, readRecords(t, data))
	}
	eng.CommitAll(Config{Consent: true, PushEveryN: 10})

	gitDir := collectionGit(t, data)
	if got := gitShow(t, gitDir, "HEAD:src/app.py"); got != "v1" {
		t.Fatalf("snapshot = %q", got)
	}
	meta := gitShow(t, gitDir, "HEAD:.trace-collector/claude/meta/claude-1a2b3c4d5e6f7081.json")
	if !strings.Contains(meta, `"host":"claude"`) {
		t.Fatalf("meta host: %s", meta)
	}
	traj := gitShow(t, gitDir, "HEAD:.trace-collector/claude/trajectory/b7e49c6d.jsonl")
	if !strings.Contains(traj, "hello") {
		t.Fatalf("transcript body: %s", traj)
	}
	if _, err := os.Stat(filepath.Join(root, ".trace-collector")); !os.IsNotExist(err) {
		t.Fatal("collector wrote into the user project")
	}
}

func TestClaudeCommitKeepsCodexTrajectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	data := t.TempDir()
	must(t, os.WriteFile(filepath.Join(data, "config.json"), []byte(`{"email":"u42@example.com","consent":true,"pushEveryN":10}`), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "hello.py"), []byte("v1"), 0o644))
	codexTranscript := filepath.Join(t.TempDir(), "rollout-codex.jsonl")
	must(t, os.WriteFile(codexTranscript, []byte("user: from codex\n"), 0o644))

	eng := &Engine{DataDir: data}
	codex := `{"hook_event_name":"Stop","session_id":"sess","turn_id":"turn-codex","cwd":"` + filepath.ToSlash(root) + `","transcript_path":"` + filepath.ToSlash(codexTranscript) + `"}`
	if res := eng.Handle([]byte(codex)); res.Action != "sealed" {
		t.Fatalf("codex seal: %+v %s", res, readRecords(t, data))
	}
	eng.CommitAll(Config{Consent: true, PushEveryN: 10})

	must(t, os.WriteFile(filepath.Join(root, "hello.py"), []byte("v2"), 0o644))
	claudeTranscript := filepath.Join(t.TempDir(), "claude.jsonl")
	must(t, os.WriteFile(claudeTranscript, []byte("{\"type\":\"user\",\"message\":{\"content\":\"from claude\"}}\n"), 0o644))
	claude := `{"hook_event_name":"Stop","session_id":"claude-sess","turn_id":"turn-claude","cwd":"` + filepath.ToSlash(root) + `","transcript_path":"` + filepath.ToSlash(claudeTranscript) + `","host":"claude"}`
	if res := eng.Handle([]byte(claude)); res.Action != "sealed" {
		t.Fatalf("claude seal: %+v %s", res, readRecords(t, data))
	}
	eng.CommitAll(Config{Consent: true, PushEveryN: 10})

	gitDir := collectionGit(t, data)
	branch := headBranch(t, gitDir)
	if got := gitShow(t, gitDir, branch+":.trace-collector/codex/trajectory/rollout-codex.jsonl"); got != "user: from codex\n" {
		t.Fatalf("codex trajectory after claude commit: %q", got)
	}
	if !strings.Contains(gitShow(t, gitDir, branch+":.trace-collector/claude/trajectory/claude.jsonl"), "from claude") {
		t.Fatal("claude trajectory missing")
	}
	codexRecords := filepath.Join(filepath.Dir(gitDir), "codex", "records.jsonl")
	claudeRecords := filepath.Join(filepath.Dir(gitDir), "claude", "records.jsonl")
	if _, err := os.Stat(codexRecords); err != nil {
		t.Fatal("codex records were not stored under the agent directory")
	}
	if _, err := os.Stat(claudeRecords); err != nil {
		t.Fatal("claude records were not stored under the agent directory")
	}
}
