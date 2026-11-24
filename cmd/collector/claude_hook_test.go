package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAdaptClaudeEventFillsTurnIDAndHost(t *testing.T) {
	in := []byte(`{"hook_event_name":"Stop","session_id":"abc","cwd":"/tmp/p","transcript_path":"/tmp/t.jsonl","stop_hook_active":false}`)
	out := adaptClaudeEvent(in)
	var ev map[string]any
	if json.Unmarshal(out, &ev) != nil {
		t.Fatalf("adapted event is not JSON: %s", out)
	}
	if ev["host"] != "claude" {
		t.Fatalf("host = %v", ev["host"])
	}
	turn, _ := ev["turn_id"].(string)
	if !strings.HasPrefix(turn, "claude-") {
		t.Fatalf("turn_id = %q", turn)
	}
	if ev["session_id"] != "abc" || ev["hook_event_name"] != "Stop" {
		t.Fatalf("original fields lost: %s", out)
	}
	if _, ok := ev["stop_hook_active"]; !ok {
		t.Fatal("claude-specific field dropped")
	}
}

func TestAdaptClaudeEventKeepsExistingValues(t *testing.T) {
	in := []byte(`{"hook_event_name":"Stop","session_id":"s","turn_id":"keep-me","host":"claude"}`)
	out := adaptClaudeEvent(in)
	var ev map[string]any
	_ = json.Unmarshal(out, &ev)
	if ev["turn_id"] != "keep-me" || ev["host"] != "claude" {
		t.Fatalf("existing values overwritten: %s", out)
	}
}

func TestAdaptClaudeEventPassesThroughBadJSON(t *testing.T) {
	in := []byte("not json")
	if string(adaptClaudeEvent(in)) != "not json" {
		t.Fatal("invalid input should pass through unchanged")
	}
}

func TestAdaptClaudeEventGeneratesUniqueTurnIDs(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		out := adaptClaudeEvent([]byte(`{"hook_event_name":"Stop","session_id":"s"}`))
		var ev map[string]any
		_ = json.Unmarshal(out, &ev)
		turn, _ := ev["turn_id"].(string)
		if turn == "" || seen[turn] {
			t.Fatalf("turn id repeated or empty: %q", turn)
		}
		seen[turn] = true
	}
}

func TestIsAgentProcessName(t *testing.T) {
	for _, name := range []string{"codex", "Codex.exe", "claude", "claude.exe", "opencode", "OpenCode.exe", "node", "node.exe"} {
		if !isAgentProcessName(name) {
			t.Fatalf("%s should match an agent process", name)
		}
	}
	for _, name := range []string{"sh", "bash", "cmd.exe", "powershell.exe", "git.exe"} {
		if isAgentProcessName(name) {
			t.Fatalf("%s should not match an agent process", name)
		}
	}
}
