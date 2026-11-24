package engine

import (
	"os/exec"
	"strings"
	"sync"
)

var (
	opencodeVersionOnce sync.Once
	opencodeVersionText string
)

// opencodeVersion runs `opencode --version` once per daemon process.
func (e *Engine) opencodeVersion() string {
	opencodeVersionOnce.Do(func() {
		out, err := exec.Command("opencode", "--version").Output()
		if err != nil {
			return
		}
		text := strings.TrimSpace(string(out))
		if i := strings.IndexAny(text, "\r\n"); i >= 0 {
			text = strings.TrimSpace(text[:i])
		}
		opencodeVersionText = text
	})
	return opencodeVersionText
}
