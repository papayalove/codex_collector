package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Claude Code support. Claude hook events use the same JSON shape the engine
// already reads (hook_event_name, session_id, cwd, transcript_path), so they
// flow through the same collection pipeline. Only version detection and the
// config home differ between hosts.

// hostLabel normalizes the host tag stored in the manifest metadata. Events
// from the Codex plugin carry no tag and keep their historical value.
func hostLabel(host string) string {
	if strings.TrimSpace(host) == "" {
		return "codex"
	}
	return host
}

// agentVersion reports the CLI version of the host that produced the event.
func (e *Engine) agentVersion(host string) string {
	if hostLabel(host) == "claude" {
		return e.claudeVersion()
	}
	if hostLabel(host) == "opencode" {
		return e.opencodeVersion()
	}
	return e.codexVersion()
}

// claudeHome returns the Claude Code config directory. CLAUDE_CONFIG_DIR moves
// it elsewhere; the default is ~/.claude.
func (e *Engine) claudeHome() string {
	if v := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); v != "" {
		return v
	}
	home := e.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return filepath.Join(home, ".claude")
}

var (
	claudeVersionOnce sync.Once
	claudeVersionText string
)

// claudeVersion runs `claude --version` once per daemon process. The CLI is the
// only reliable source of its own version, and exec'ing it per commit would be
// too slow, so the first commit of a daemon pays the cost and the rest reuse it.
func (e *Engine) claudeVersion() string {
	claudeVersionOnce.Do(func() {
		cmd := exec.Command("claude", "--version")
		out, err := cmd.Output()
		if err != nil {
			return
		}
		text := strings.TrimSpace(string(out))
		if i := strings.IndexAny(text, "\r\n"); i >= 0 {
			text = strings.TrimSpace(text[:i])
		}
		claudeVersionText = text
	})
	return claudeVersionText
}
