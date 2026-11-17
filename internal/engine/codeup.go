package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

var codeupOrgID = regexp.MustCompile(`^[0-9a-fA-F]{24}$`)

type codeupClient struct {
	API   string
	Token string
	HTTP  *http.Client
}

type codeupTarget struct {
	Org   string
	Group string
}

func codeupToken() string {
	if v := strings.TrimSpace(os.Getenv("CODEUP_TOKEN")); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("YUNXIAO_TOKEN"))
}

func codeupAPI() string {
	if v := strings.TrimSpace(os.Getenv("CODEUP_DOMAIN")); v != "" {
		if strings.Contains(v, "://") {
			return strings.TrimRight(v, "/")
		}
		return "https://" + strings.TrimRight(v, "/")
	}
	return "https://openapi-rdc.aliyuncs.com"
}

func codeupFromNamespace(namespace string) (codeupTarget, bool) {
	host, path, ok := splitGitNamespace(namespace)
	if !ok || !isCodeupHost(host) {
		return codeupTarget{}, false
	}
	parts := splitPath(path)
	org := strings.TrimSpace(os.Getenv("CODEUP_ORG_ID"))
	if org == "" {
		org = strings.TrimSpace(os.Getenv("CODEUP_ORGANIZATION_ID"))
	}
	if len(parts) > 0 && codeupOrgID.MatchString(parts[0]) {
		org = parts[0]
		parts = parts[1:]
	}
	return codeupTarget{Org: org, Group: strings.Join(parts, "/")}, true
}

func isCodeupHost(host string) bool {
	host = strings.ToLower(host)
	return host == "codeup.aliyun.com" || strings.HasSuffix(host, ".codeup.aliyun.com")
}

func splitGitNamespace(namespace string) (host, path string, ok bool) {
	s := strings.TrimSpace(namespace)
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimRight(s, "/")
	switch {
	case strings.HasPrefix(s, "https://"):
		s = strings.TrimPrefix(s, "https://")
	case strings.HasPrefix(s, "http://"):
		s = strings.TrimPrefix(s, "http://")
	case strings.HasPrefix(s, "git@"):
		s = strings.TrimPrefix(s, "git@")
		host, path, ok = strings.Cut(s, ":")
		return strings.TrimSpace(host), strings.Trim(path, "/"), ok && host != ""
	default:
		return "", "", false
	}
	host, path, ok = strings.Cut(s, "/")
	if i := strings.Index(host, "@"); i >= 0 {
		host = host[i+1:]
	}
	return strings.TrimSpace(host), strings.Trim(path, "/"), host != ""
}

func splitPath(path string) []string {
	var parts []string
	for _, part := range strings.Split(path, "/") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func (c codeupClient) api() string {
	if c.API != "" {
		return strings.TrimRight(c.API, "/")
	}
	return codeupAPI()
}

func (c codeupClient) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (c codeupClient) do(method, path string, body any) (*http.Response, error) {
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.api()+path, buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "codex-collector")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("x-yunxiao-token", c.Token)
	}
	return c.http().Do(req)
}

func ensureRemoteProject(namespace, name string) error {
	if owner, ok := githubOwner(namespace); ok {
		return ensureGitHubRepo(githubClient{Token: githubToken()}, owner, name)
	}
	if spec, ok := codeupFromNamespace(namespace); ok {
		return ensureCodeupRepo(codeupClient{Token: codeupToken()}, spec, name)
	}
	return nil
}

// ensureCodeupRepo creates the collection repository when it does not exist.
func ensureCodeupRepo(c codeupClient, spec codeupTarget, name string) error {
	if c.Token == "" {
		return fmt.Errorf("未配置 CODEUP_TOKEN")
	}
	if spec.Org == "" {
		return fmt.Errorf("未配置 Codeup 组织 ID")
	}
	full := name
	if spec.Group != "" {
		full = spec.Group + "/" + name
	}
	base := "/oapi/v1/codeup/organizations/" + url.PathEscape(spec.Org)
	resp, err := c.do(http.MethodGet, base+"/repositories/"+url.PathEscape(full), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode != http.StatusNotFound {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("查询 Codeup 仓库失败: %s %s", resp.Status, bytes.TrimSpace(msg))
	}

	body := map[string]any{
		"name":        name,
		"path":        name,
		"visibility":  "private",
		"description": "codex collector",
	}
	query := ""
	if spec.Group != "" {
		nsResp, err := c.do(http.MethodGet, base+"/namespaces/"+url.PathEscape(spec.Group), nil)
		if err != nil {
			return err
		}
		defer nsResp.Body.Close()
		if nsResp.StatusCode == http.StatusOK {
			var ns struct {
				ID int64 `json:"id"`
			}
			if err := json.NewDecoder(nsResp.Body).Decode(&ns); err != nil {
				return err
			}
			if ns.ID != 0 {
				body["namespaceId"] = ns.ID
			}
		} else {
			_, _ = io.Copy(io.Discard, nsResp.Body)
			body["path"] = spec.Group + "/" + name
			query = "?createParentPath=true"
		}
	}
	create, err := c.do(http.MethodPost, base+"/repositories"+query, body)
	if err != nil {
		return err
	}
	defer create.Body.Close()
	if create.StatusCode == http.StatusOK || create.StatusCode == http.StatusCreated || create.StatusCode == http.StatusConflict {
		return nil
	}
	msg, _ := io.ReadAll(io.LimitReader(create.Body, 512))
	text := strings.ToLower(string(msg))
	if strings.Contains(text, "already") || strings.Contains(string(msg), "已存在") {
		return nil
	}
	return fmt.Errorf("创建 Codeup 仓库失败: %s %s", create.Status, bytes.TrimSpace(msg))
}
