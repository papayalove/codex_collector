package engine

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type trajFile struct {
	Name   string
	Body   []byte
	Source string
}

// collectTrajectories copies the session rollout as raw JSONL.
// Codex versions differ in where that file lives, not in the container format:
// dated sessions, flat archived_sessions, .jsonl.zst, and a sibling file for a child rollout.
func (e *Engine) collectTrajectories(ev hookEvent, repoKey string) []trajFile {
	paths := e.resolveRollouts(ev)
	var out []trajFile
	seen := map[string]bool{}
	for _, path := range paths {
		body, err := readRollout(path)
		if err != nil {
			appendRecord(e.DataDir, Record{RepoKey: repoKey, SessionID: ev.SessionID, TurnID: ev.TurnID, Host: ev.Host, Reason: reasonFailed, Detail: err.Error()})
			continue
		}
		body = redact(body)
		name := storedRolloutName(path)
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, trajFile{Name: name, Body: body, Source: path})
	}
	return out
}

func (e *Engine) resolveRollouts(ev hookEvent) []string {
	primary := ""
	if ev.Transcript != "" {
		primary = preferPlain(ev.Transcript)
		if _, err := os.Stat(primary); err != nil {
			primary = ""
		}
	}
	if primary == "" {
		primary = e.findRollout(ev.SessionID, filepath.Base(ev.Transcript))
	}
	if primary == "" {
		return nil
	}
	paths := []string{primary}
	paths = append(paths, siblingRollouts(primary)...)
	return uniqueRollouts(paths)
}

func (e *Engine) codexHome() string {
	if v := strings.TrimSpace(os.Getenv("CODEX_HOME")); v != "" {
		return v
	}
	home := e.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return filepath.Join(home, ".codex")
}

func (e *Engine) codexVersion() string {
	b, err := os.ReadFile(filepath.Join(e.codexHome(), "version.json"))
	if err != nil {
		return ""
	}
	var v struct {
		Latest string `json:"latest_version"`
	}
	if json.Unmarshal(b, &v) != nil {
		return ""
	}
	return strings.TrimSpace(v.Latest)
}

func (e *Engine) findRollout(sessionID, base string) string {
	sessionID = strings.TrimSpace(sessionID)
	base = strings.TrimSpace(base)
	if sessionID == "" && base == "" {
		return ""
	}
	var hits []string
	for _, sub := range []string{"sessions", "archived_sessions"} {
		root := filepath.Join(e.codexHome(), sub)
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			name := d.Name()
			if !isRolloutName(name) {
				return nil
			}
			if sessionID != "" && strings.Contains(name, sessionID) {
				hits = append(hits, path)
				return nil
			}
			if base != "" && name == base {
				hits = append(hits, path)
			}
			return nil
		})
	}
	return chooseRollout(hits, sessionID)
}

func siblingRollouts(primary string) []string {
	dir := filepath.Dir(primary)
	stem := rolloutStem(filepath.Base(primary))
	matches, _ := filepath.Glob(filepath.Join(dir, stem+"_*"))
	var out []string
	for _, match := range matches {
		name := filepath.Base(match)
		if isRolloutName(name) {
			out = append(out, preferPlain(match))
		}
	}
	subagents, _ := filepath.Glob(filepath.Join(dir, "subagents", "*"))
	for _, match := range subagents {
		if isRolloutName(filepath.Base(match)) {
			out = append(out, preferPlain(match))
		}
	}
	return out
}

func chooseRollout(paths []string, sessionID string) string {
	best := ""
	bestRank := 0
	for _, path := range paths {
		path = preferPlain(path)
		rank := rolloutRank(path)
		stem := rolloutStem(filepath.Base(path))
		if sessionID != "" && strings.Contains(stem, "_") {
			rank += 4
		}
		if best == "" || rank < bestRank {
			best = path
			bestRank = rank
		}
	}
	return best
}

func rolloutRank(path string) int {
	rank := 0
	if strings.HasSuffix(strings.ToLower(path), ".zst") {
		rank += 2
	}
	if strings.Contains(filepath.ToSlash(path), "/archived_sessions/") {
		rank += 1
	}
	return rank
}

func uniqueRollouts(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, path := range paths {
		path = preferPlain(path)
		if path == "" || seen[path] {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func preferPlain(path string) string {
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".jsonl.zst") {
		plain := path[:len(path)-len(".zst")]
		if _, err := os.Stat(plain); err == nil {
			return plain
		}
	}
	return path
}

func isRolloutName(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".jsonl") || strings.HasSuffix(lower, ".jsonl.zst")
}

func rolloutStem(name string) string {
	lower := strings.ToLower(name)
	name = strings.TrimSuffix(name, ".zst")
	if strings.HasSuffix(lower, ".jsonl") || strings.HasSuffix(lower, ".jsonl.zst") {
		name = strings.TrimSuffix(name, ".jsonl")
	}
	return name
}

func storedRolloutName(path string) string {
	name := filepath.Base(preferPlain(path))
	name = strings.TrimSuffix(name, ".zst")
	if !strings.HasSuffix(strings.ToLower(name), ".jsonl") {
		name += ".jsonl"
	}
	return safeName(name)
}

func readRollout(path string) ([]byte, error) {
	if strings.HasSuffix(strings.ToLower(path), ".zst") {
		cmd := exec.Command("zstd", "-dc", "--quiet", path)
		return cmd.Output()
	}
	return os.ReadFile(path)
}
