package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var shellTokenKeys = []string{"GITHUB_TOKEN", "GH_TOKEN", "CODEUP_TOKEN", "YUNXIAO_TOKEN"}

func daemonEnviron() []string {
	env := os.Environ()
	for _, pair := range shellTokenEnv() {
		env = append(env, pair)
	}
	return env
}

// applyShellTokens loads tokens missing from this process into its own environment.
// The daemon calls this at startup, so a later GitHub request sees GITHUB_TOKEN
// even when the parent process never had it.
func applyShellTokens() {
	for _, pair := range shellTokenEnv() {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || key == "" || value == "" {
			continue
		}
		_ = os.Setenv(key, value)
	}
}

func currentTokenStamp() string {
	pairs := shellTokenEnv()
	if len(pairs) == 0 {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join(pairs, "\n")))
	return hex.EncodeToString(sum[:8])
}

// shellTokenEnv reads token assignments that the login shell would export,
// when the current process was started without them. The returned values are
// only placed into the daemon process environment.
func shellTokenEnv() []string {
	needed := map[string]bool{}
	for _, key := range shellTokenKeys {
		if strings.TrimSpace(os.Getenv(key)) == "" {
			needed[key] = true
		}
	}
	if len(needed) == 0 {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	var files []string
	for _, name := range []string{".bashrc", ".bash_profile", ".profile", ".zshrc"} {
		files = append(files, filepath.Join(home, name))
	}
	entries, _ := os.ReadDir(filepath.Join(home, ".config", "environment.d"))
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".conf") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		files = append(files, filepath.Join(home, ".config", "environment.d", name))
	}
	found := map[string]string{}
	for _, path := range files {
		for key, value := range tokensInFile(path) {
			if needed[key] && found[key] == "" {
				found[key] = value
			}
		}
	}
	var pairs []string
	for _, key := range shellTokenKeys {
		if found[key] != "" {
			pairs = append(pairs, key+"="+found[key])
		}
	}
	return pairs
}

func tokensInFile(path string) map[string]string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	found := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		key, value, ok := parseTokenLine(line)
		if ok && found[key] == "" {
			found[key] = value
		}
	}
	return found
}

func parseTokenLine(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	if strings.HasPrefix(line, "export ") || strings.HasPrefix(line, "export\t") {
		line = strings.TrimSpace(line[len("export"):])
	}
	key, raw, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	if !isShellTokenKey(key) {
		return "", "", false
	}
	value := shellValue(strings.TrimSpace(raw))
	if value == "" || strings.Contains(value, "$") {
		return "", "", false
	}
	return key, value, true
}

func isShellTokenKey(key string) bool {
	for _, name := range shellTokenKeys {
		if key == name {
			return true
		}
	}
	return false
}

func shellValue(raw string) string {
	if raw == "" {
		return ""
	}
	if raw[0] == '"' || raw[0] == '\'' {
		quote := raw[0]
		rest := raw[1:]
		end := strings.IndexByte(rest, quote)
		if end < 0 {
			return ""
		}
		return rest[:end]
	}
	if i := strings.IndexAny(raw, " \t#"); i >= 0 {
		raw = strings.TrimSpace(raw[:i])
	}
	return raw
}
