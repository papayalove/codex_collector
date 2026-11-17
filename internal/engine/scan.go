package engine

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var secretLine = regexp.MustCompile(`(?i)-----BEGIN [A-Z ]*PRIVATE KEY-----|AKIA[0-9A-Z]{16}|(api[_-]?key|secret|token)\s*[:=]\s*\S{8,}`)

var largeSuffix = map[string]bool{
	".pt": true, ".pth": true, ".onnx": true, ".safetensors": true, ".gguf": true,
	".ckpt": true, ".h5": true, ".zip": true, ".tar": true, ".gz": true, ".tgz": true,
	".7z": true, ".rar": true, ".parquet": true, ".arrow": true, ".npy": true, ".npz": true,
}

type scanned struct {
	rel   string
	abs   string
	mode  string
	size  int64
	mtime int64
	kind  string
	link  string
}

type manifestItem struct {
	Path   string `json:"path"`
	Remote string `json:"remote"`
	Reason string `json:"reason,omitempty"`
}

func isSecretName(name string) bool {
	low := strings.ToLower(name)
	switch {
	case low == ".env" || strings.HasPrefix(low, ".env."):
		return true
	case strings.HasSuffix(low, ".pem") || strings.Contains(low, "id_rsa"):
		return true
	case strings.Contains(low, "credential") && strings.HasSuffix(low, ".json"):
		return true
	default:
		return false
	}
}

func isLargePath(rel string, size, max int64) bool {
	low := strings.ToLower(filepath.ToSlash(rel))
	if largeSuffix[strings.ToLower(filepath.Ext(low))] {
		return true
	}
	return max > 0 && size > max
}

func classifyContent(it scanned, maxBytes int64) scanned {
	if it.kind == "symlink" {
		return it
	}
	if it.kind == "large" || isLargePath(it.rel, it.size, maxBytes) {
		it.kind = "large"
		return it
	}
	name := it.rel
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if isSecretName(name) || contentSecret(it.abs) {
		it.kind = "secret"
		return it
	}
	it.kind = "file"
	it.mode = "100644"
	return it
}

func contentSecret(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 16<<10)
	n, _ := f.Read(buf)
	return secretLine.Match(buf[:n])
}

func scanProject(root string, maxFiles int, maxBytes int64) ([]scanned, string, error) {
	return walkProject(root, maxFiles, maxBytes, true)
}

// scanMeta records size and mtime without reading file contents.
func scanMeta(root string, maxFiles int, maxBytes int64) ([]scanned, string, error) {
	return walkProject(root, maxFiles, maxBytes, false)
}

func walkProject(root string, maxFiles int, maxBytes int64, readContent bool) ([]scanned, string, error) {
	root = filepath.Clean(root)
	var items []scanned
	var stop string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == ".codex-collect" {
				return filepath.SkipDir
			}
			return nil
		}
		if len(items) >= maxFiles {
			stop = "文件数超过 2 万"
			return io.EOF
		}
		rel := relSlash(root, path)
		info, err := d.Info()
		if err != nil {
			return nil
		}
		item := scanned{rel: rel, abs: path, size: info.Size(), mtime: info.ModTime().UnixNano(), mode: "100644", kind: "file"}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return nil
			}
			item.kind = "symlink"
			item.mode = "120000"
			item.link = target
			items = append(items, item)
			return nil
		}
		if isLargePath(rel, info.Size(), maxBytes) {
			item.kind = "large"
			items = append(items, item)
			return nil
		}
		if isSecretName(name) || (readContent && contentSecret(path)) {
			item.kind = "secret"
			items = append(items, item)
			return nil
		}
		items = append(items, item)
		return nil
	})
	if err != nil && err != io.EOF {
		return nil, "", err
	}
	return items, stop, nil
}

func snapshotRoot(cwd string) (string, string) {
	cwd = canonical(cwd)
	if home, err := os.UserHomeDir(); err == nil && cwd == canonical(home) {
		return cwd, "家目录"
	}
	dir := filepath.FromSlash(cwd)
	for {
		gitPath := filepath.Join(dir, ".git")
		if st, err := os.Stat(gitPath); err == nil && (st.IsDir() || st.Mode().IsRegular()) {
			return canonical(dir), ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return cwd, ""
}

func userBranch(project string) (branch, head string) {
	dir := filepath.FromSlash(project)
	gitPath := filepath.Join(dir, ".git")
	if _, err := os.Stat(gitPath); err != nil {
		return "nogit", ""
	}
	name, err := gitOutput(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "nogit", ""
	}
	head, err = gitOutput(dir, "rev-parse", "HEAD")
	if err != nil {
		return "nogit", ""
	}
	if name == "HEAD" || name == "" {
		short := head
		if len(short) > 12 {
			short = short[:12]
		}
		return "detached-" + short, head
	}
	return name, head
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func ancestor(project, older, newer string) bool {
	if older == "" || newer == "" || older == newer {
		return older != "" && older == newer
	}
	_, err := gitOutput(filepath.FromSlash(project), "merge-base", "--is-ancestor", older, newer)
	return err == nil
}
