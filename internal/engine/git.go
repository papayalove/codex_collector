package engine

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type gitRunner struct {
	bin string
}

func (g gitRunner) look() string {
	if g.bin != "" {
		return g.bin
	}
	p, err := exec.LookPath("git")
	if err != nil {
		return "git"
	}
	return p
}

func (g gitRunner) run(gitDir string, extraEnv []string, args ...string) (string, error) {
	cmd := exec.Command(g.look(), args...)
	cmd.Env = append(os.Environ(), "GIT_DIR="+gitDir)
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return strings.TrimSpace(string(out)), nil
}

func (g gitRunner) init(gitDir string) error {
	marker := filepath.Join(gitDir, "codex-collect-ready")
	if _, err := os.Stat(marker); err == nil {
		return nil
	}
	if _, err := os.Stat(gitDir); err != nil {
		parent := filepath.Dir(gitDir)
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return err
		}
		cmd := exec.Command(g.look(), "init", parent)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("git init: %w: %s", err, bytes.TrimSpace(out))
		}
	}
	if _, err := g.run(gitDir, nil, "config", "core.autocrlf", "false"); err != nil {
		return err
	}
	if _, err := g.run(gitDir, nil, "config", "core.quotepath", "false"); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte("1\n"), 0o644)
}

func (g gitRunner) hashStdin(gitDir string, data []byte) (string, error) {
	cmd := exec.Command(g.look(), "hash-object", "-w", "--stdin")
	cmd.Env = append(os.Environ(), "GIT_DIR="+gitDir)
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("hash-object: %w: %s", err, bytes.TrimSpace(out))
	}
	return strings.TrimSpace(string(out)), nil
}

func (g gitRunner) hashFiles(gitDir string, absPaths []string) ([]string, error) {
	if len(absPaths) == 0 {
		return nil, nil
	}
	var input bytes.Buffer
	for _, p := range absPaths {
		input.WriteString(p)
		input.WriteByte('\n')
	}
	cmd := exec.Command(g.look(), "hash-object", "-w", "--stdin-paths")
	cmd.Env = append(os.Environ(), "GIT_DIR="+gitDir)
	cmd.Stdin = &input
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("hash-object paths: %w: %s", err, bytes.TrimSpace(out))
	}
	lines := strings.Fields(string(out))
	if len(lines) != len(absPaths) {
		return nil, fmt.Errorf("hash-object 返回 %d 个对象，期望 %d", len(lines), len(absPaths))
	}
	return lines, nil
}

func (g gitRunner) writeTree(gitDir, indexPath string, info []byte) (string, error) {
	_ = os.Remove(indexPath)
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		return "", err
	}
	cmd := exec.Command(g.look(), "update-index", "-z", "--index-info")
	cmd.Env = append(os.Environ(), "GIT_DIR="+gitDir, "GIT_INDEX_FILE="+indexPath)
	cmd.Stdin = bytes.NewReader(info)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("update-index: %w: %s", err, bytes.TrimSpace(out))
	}
	cmd = exec.Command(g.look(), "write-tree")
	cmd.Env = append(os.Environ(), "GIT_DIR="+gitDir, "GIT_INDEX_FILE="+indexPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("write-tree: %w: %s", err, bytes.TrimSpace(out))
	}
	return strings.TrimSpace(string(out)), nil
}

func (g gitRunner) commitTree(gitDir, tree, parent, message string) (string, error) {
	args := []string{"-c", "user.name=codex-collector", "-c", "user.email=collector@local", "commit-tree", tree, "-F", "-"}
	if parent != "" {
		args = []string{"-c", "user.name=codex-collector", "-c", "user.email=collector@local", "commit-tree", tree, "-p", parent, "-F", "-"}
	}
	cmd := exec.Command(g.look(), args...)
	cmd.Env = append(os.Environ(), "GIT_DIR="+gitDir)
	cmd.Stdin = strings.NewReader(message)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("commit-tree: %w: %s", err, bytes.TrimSpace(out))
	}
	return strings.TrimSpace(string(out)), nil
}

func (g gitRunner) revParse(gitDir, rev string) (string, error) {
	out, err := g.run(gitDir, nil, "rev-parse", rev)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

type treeItem struct {
	Mode string
	SHA  string
	Path string
}

func (g gitRunner) listTree(gitDir, rev string) ([]treeItem, error) {
	out, err := g.run(gitDir, nil, "ls-tree", "-r", "-z", rev)
	if err != nil {
		return nil, err
	}
	var items []treeItem
	rest := []byte(out)
	for len(rest) > 0 {
		rec, next, ok := bytes.Cut(rest, []byte{0})
		rest = next
		if len(rec) == 0 {
			if !ok {
				break
			}
			continue
		}
		tab := bytes.IndexByte(rec, '\t')
		if tab < 0 {
			continue
		}
		meta := strings.Fields(string(rec[:tab]))
		if len(meta) < 3 {
			continue
		}
		items = append(items, treeItem{Mode: meta[0], SHA: meta[2], Path: string(rec[tab+1:])})
	}
	return items, nil
}

func (g gitRunner) treeFromEntries(gitDir, indexPath string, entries []treeItem) (string, error) {
	var info []byte
	for _, ent := range entries {
		info = append(info, indexInfo(ent.Mode, ent.SHA, ent.Path)...)
	}
	return g.writeTree(gitDir, indexPath, info)
}

func (g gitRunner) treeWithFile(gitDir, parentTree, indexPath, path string, content []byte) (string, error) {
	sha, err := g.hashStdin(gitDir, content)
	if err != nil {
		return "", err
	}
	var info []byte
	if parentTree != "" {
		out, err := g.run(gitDir, nil, "ls-tree", "-r", "-z", parentTree)
		if err != nil {
			return "", err
		}
		rest := []byte(out)
		for len(rest) > 0 {
			rec, next, ok := bytes.Cut(rest, []byte{0})
			rest = next
			if len(rec) == 0 {
				if !ok {
					break
				}
				continue
			}
			tab := bytes.IndexByte(rec, '\t')
			if tab < 0 {
				continue
			}
			meta := strings.Fields(string(rec[:tab]))
			name := string(rec[tab+1:])
			if len(meta) < 3 || name == path {
				continue
			}
			info = append(info, indexInfo(meta[0], meta[2], name)...)
		}
	}
	info = append(info, indexInfo("100644", sha, path)...)
	return g.writeTree(gitDir, indexPath, info)
}

func (g gitRunner) countBytes(gitDir string) (int64, error) {
	out, err := g.run(gitDir, nil, "count-objects", "-v")
	if err != nil {
		return 0, err
	}
	return parseCountObjects(out), nil
}
