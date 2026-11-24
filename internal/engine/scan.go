package engine

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// hasSecret reports whether a trajectory line, or the first 16KB of a file,
// contains a secret. A match redacts that trajectory line, or keeps the file
// out of the commit. Known token shapes follow tools/.gitleaks.toml. A
// keyword assignment counts only when the value looks like a credential.
var knownSecret = regexp.MustCompile(knownSecretPattern)
var keywordSecret = regexp.MustCompile(keywordSecretPattern)

const knownSecretPattern = `` +
	`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----` +
	`|PuTTY-User-Key-File-` +
	`|(?:AKIA|ASIA|AIDA|AGPA|AROA|AIPA|ANPA|ANVA)[0-9A-Z]{16}` +
	`|\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b` +
	`|\bglpat-[A-Za-z0-9_\-]{20,}\b` +
	`|\bxox[abprs]-[A-Za-z0-9\-]{10,}\b` +
	`|hooks\.slack\.com/services/T[A-Za-z0-9]+/` +
	`|\b(?:sk|rk|pk)_(?:live|test)_[A-Za-z0-9]{16,}\b` +
	`|\bAIza[0-9A-Za-z_\-]{35}\b` +
	`|\bya29\.[0-9A-Za-z_\-]+` +
	`|\bsk-(?:proj-|ant-)?[A-Za-z0-9_\-]{16,}\b` +
	`|\bhf_[A-Za-z0-9]{20,}\b` +
	`|\bpypi-[A-Za-z0-9_\-]{20,}\b` +
	`|\bnpm_[A-Za-z0-9]{20,}\b` +
	`|\bshpat_[A-Za-z0-9]{20,}\b` +
	`|\blin_api_[A-Za-z0-9]{20,}\b` +
	`|\bkey-[0-9A-Za-z]{32}\b` +
	`|\bSG\.[A-Za-z0-9_\-]{16,}\.[A-Za-z0-9_\-]{16,}\b` +
	`|\beyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\b` +
	`|(?i:bearer)\s+[A-Za-z0-9\-._~+/]{16,}={0,2}` +
	`|(?i:postgres(?:ql)?|mysql|mongodb(?:\+srv)?|redis|amqp)://[^\s:/]+:[^\s@]{8,}@`

const keywordValue = `([A-Za-z0-9+/=_\-.~]{8,})`

const keywordSecretPattern = `(?i:` +
	`\b(?:api[ _-]?key|secret[ _-]?key|access[ _-]?key|private[ _-]?key|client[ _-]?secret|auth(?:entication)?[ _-]?token|refresh[ _-]?token|session[ _-]?token|password|passwd|pwd|secret|token|credential)\b["']?(?:\s*(?:is|是|为)\s*|\s*[:：=]\s*|\s+)["']?` + keywordValue +
	`|(?:访问密钥|私钥|密钥|秘钥|令牌|密码|口令|凭证|凭据)["']?(?:\s*(?:是|为)\s*|\s*[:：=]\s*|\s+)["']?` + keywordValue +
	`|["']?\b(?:ak|sk)\b["']?\s*[:=]\s*["']?` + keywordValue +
	`)`

func hasSecret(b []byte) bool {
	if knownSecret.Match(b) {
		return true
	}
	for _, loc := range keywordSecret.FindAllSubmatchIndex(b, -1) {
		for i := 2; i+1 < len(loc); i += 2 {
			if loc[i] >= 0 && credentialValue(b[loc[i]:loc[i+1]]) {
				return true
			}
		}
	}
	return false
}

func hasSecretString(s string) bool {
	return hasSecret([]byte(s))
}

func credentialValue(v []byte) bool {
	if len(v) >= 24 {
		return true
	}
	if len(v) < 8 {
		return false
	}
	var letter, digit bool
	for _, c := range v {
		switch {
		case c >= '0' && c <= '9':
			digit = true
		case (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z'):
			letter = true
		}
	}
	return letter && digit
}

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
	return hasSecret(buf[:n])
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
			if name == ".git" || name == ".codex-collect" || name == ".trace-collector" {
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
