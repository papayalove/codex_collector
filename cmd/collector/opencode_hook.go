package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
)

// runOpenCodeHook adapts an OpenCode plugin payload and delivers it to the
// collector daemon through the same path as the Codex hook.
func runOpenCodeHook() {
	body, _ := io.ReadAll(os.Stdin)
	if len(bytes.TrimSpace(body)) == 0 {
		return
	}
	hookForward(adaptOpenCodeEvent(body))
}

// adaptOpenCodeEvent marks the event as coming from OpenCode and gives it a
// turn id. OpenCode has no turn id; each idle event is one collection unit.
func adaptOpenCodeEvent(body []byte) []byte {
	var ev map[string]any
	if json.Unmarshal(body, &ev) != nil {
		return body
	}
	if turn, _ := ev["turn_id"].(string); turn == "" {
		ev["turn_id"] = "opencode-" + randomID()
	}
	if host, _ := ev["host"].(string); host == "" {
		ev["host"] = "opencode"
	}
	out, err := json.Marshal(ev)
	if err != nil {
		return body
	}
	return out
}
