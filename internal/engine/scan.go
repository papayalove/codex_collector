package engine

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// secretLine matches a whole trajectory line or the first 16KB of a file.
// A match redacts that trajectory line, or keeps the file out of the commit.
var secretLine = regexp.MustCompile(secretPattern)

const secretPattern = `(?i)` +
	`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----` +
	`|PuTTY-User-Key-File-` +
	`|AKIA[0-9A-Z]{16}|ASIA[0-9A-Z]{16}` +
	`|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}` +
	`|glpat-[A-Za-z0-9_\-]{20,}` +
	`|xox[baprs]-[A-Za-z0-9\-]{10,}` +
	"|hooks\\.slack\\.com/services/T[A-Za-z0-9]+/" +
	`|sk_(?:live|test)_[A-Za-z0-9]{10,}|rk_(?:live|test)_[A-Za-z0-9]{10,}` +
	`|AIza[0-9A-Za-z_\-]{20,}` +
	`|ya29\.[0-9A-Za-z_\-]+` +
	`|sk-(?:proj-|ant-)?[A-Za-z0-9_\-]{20,}` +
	`|hf_[A-Za-z0-9]{20,}` +
	`|pypi-[A-Za-z0-9_\-]{20,}` +
	`|npm_[A-Za-z0-9]{20,}` +
	`|SG\.[A-Za-z0-9_\-]{20,}` +
	`|eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}` +
	"|bearer\\s+[A-Za-z0-9\\-._~+/]{16,}={0,2}" +
	"|(?:postgres(?:ql)?|mysql|mongodb(?:\\+srv)?|redis|amqp)://[^\\s:/]+:[^\\s@]{8,}@" +
	"|[\"']?(?:api[ _-]?key|secret[ _-]?key|access[ _-]?key|private[ _-]?key|client[ _-]?secret|auth(?:entication)?[ _-]?token|refresh[ _-]?token|session[ _-]?token|password|passwd|pwd|secret|token|credential)[\"']?\\s*[:=]\\s*[\"']?[A-Za-z0-9+/=_\\-.~]{8,}" +
	"|(?:api[ _-]?key|secret|token|password|passwd)\\s+is\\s+[A-Za-z0-9+/=_\\-.~]{8,}" +
	"|(?:password|passwd|pwd)\\s*(?:is\\s*|[:=]\\s*)?(?:[A-Za-z0-9+/=_\\-.~]{8,}|\\d{6,})" +
	"|(?:访问密钥|私钥|密钥|秘钥|令牌|密码|口令|凭证|凭据)\\s*(?:是|为|[:：=])?\\s*(?:[A-Za-z0-9+/=_\\-.~]{8,}|\\d{6,})" +
	`|\b(?:ak|sk)\b["']?\s*[:=]\s*["']?[A-Za-z0-9+/=_\-.~]{8,}` +
	`|\bsecret[_-]?key\b["']?\s*(?:is\s*|是\s*|为\s*|[:：=]\s*)["']?(?:[A-Za-z0-9+/=_\-.~]{8,}|\d{6,})` +
	`|\bsecret[_-]?key\b["']?\s+["']?(?:\d[A-Za-z0-9+/=_\-.~]{7,}|[A-Za-z+/=_\-.~]+\d[A-Za-z0-9+/=_\-.~]{6,}|\d{6,})` +
	`|\bsecret key\b["']?\s*(?:is\s*|是\s*|为\s*|[:：=]\s*)["']?(?:[A-Za-z0-9+/=_\-.~]{8,}|\d{6,})` +
	`|\bsecret key\b["']?\s+["']?(?:\d[A-Za-z0-9+/=_\-.~]{7,}|[A-Za-z+/=_\-.~]+\d[A-Za-z0-9+/=_\-.~]{6,}|\d{6,})`

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
