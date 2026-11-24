package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"
)

// runClaudeHook adapts a Claude Code hook payload and delivers it to the
// collector daemon through the same path as the Codex hook.
func runClaudeHook() {
	body, _ := io.ReadAll(os.Stdin)
	if len(bytes.TrimSpace(body)) == 0 {
		return
	}
	hookForward(adaptClaudeEvent(body))
}

// adaptClaudeEvent fills the fields a Claude Code event does not carry: a turn
// id, because Claude Code has no turn concept and every Stop starts its own
// collection unit, and a host tag so the collected metadata records which
// agent produced the session.
func adaptClaudeEvent(body []byte) []byte {
	var ev map[string]any
	if json.Unmarshal(body, &ev) != nil {
		return body
	}
	if turn, _ := ev["turn_id"].(string); turn == "" {
		ev["turn_id"] = "claude-" + randomID()
	}
	if host, _ := ev["host"].(string); host == "" {
		ev["host"] = "claude"
	}
	out, err := json.Marshal(ev)
	if err != nil {
		return body
	}
	return out
}

func randomID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// hookForward makes sure the daemon that belongs to the current host process is
// running, then delivers one hook payload to it.
func hookForward(body []byte) {
	withDaemonLock(func() { ensureCurrentDaemon() })
	if err := call(body); err != nil {
		withDaemonLock(func() {
			owner := collectorOwnerPID()
			info, loadErr := loadDaemonInfo()
			if loadErr != nil || info.OwnerPid != owner || !processAlive(info.Pid) {
				_ = startDaemon(owner)
			}
		})
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if call(body) == nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// isAgentProcessName reports whether a process in a hook's ancestry belongs to
// a supported host. Claude Code may run as a claude binary or as node.
func isAgentProcessName(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "codex") || strings.Contains(n, "claude") || strings.Contains(n, "opencode") || strings.Contains(n, "node")
}
