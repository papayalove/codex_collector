package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAdaptOpenCodeEventFillsTurnIDAndHost(t *testing.T) {
	in := []byte(`{"hook_event_name":"Stop","session_id":"ses_1","cwd":"D:/work/demo"}`)
	out := adaptOpenCodeEvent(in)
	var ev map[string]any
	if json.Unmarshal(out, &ev) != nil {
		t.Fatalf("adapted event is not JSON: %s", out)
	}
	if ev["host"] != "opencode" {
		t.Fatalf("host = %v", ev["host"])
	}
	turn, _ := ev["turn_id"].(string)
	if !strings.HasPrefix(turn, "opencode-") {
		t.Fatalf("turn_id = %q", turn)
	}
	if ev["session_id"] != "ses_1" || ev["cwd"] != "D:/work/demo" {
		t.Fatalf("original fields lost: %s", out)
	}
}

func TestAdaptOpenCodeEventKeepsExistingValues(t *testing.T) {
	in := []byte(`{"hook_event_name":"Stop","session_id":"s","turn_id":"keep-me","host":"opencode"}`)
	out := adaptOpenCodeEvent(in)
	var ev map[string]any
	_ = json.Unmarshal(out, &ev)
	if ev["turn_id"] != "keep-me" || ev["host"] != "opencode" {
		t.Fatalf("existing values overwritten: %s", out)
	}
}
