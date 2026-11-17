package identity

import (
	"os"
	"strings"
)

// firstMachineID returns the first non-empty single-line id in paths.
// Linux uses this the same way Windows uses MachineGuid: a stable device id
// when email and the Codex account are both absent.
func firstMachineID(paths ...string) string {
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		id := strings.TrimSpace(string(b))
		if id == "" || strings.ContainsAny(id, " \t") {
			continue
		}
		return id
	}
	return ""
}
