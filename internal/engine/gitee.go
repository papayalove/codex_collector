package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type giteeClient struct {
	API   string
	Token string
	HTTP  *http.Client
}

func giteeToken() string {
	return strings.TrimSpace(os.Getenv("GITEE_TOKEN"))
}

type giteeTarget struct {
	Host       string
	Community  bool
	Owner      string
	Enterprise string
	Group      string
}

func giteeOwner(namespace string) (string, bool) {
	spec, ok := giteeFromNamespace(namespace)
	if !ok {
		return "", false
	}
	if spec.Community {
		return spec.Owner, true
	}
	if spec.Enterprise == "" {
		return "", false
	}
	return spec.Enterprise, true
}

func giteeFromNamespace(namespace string) (giteeTarget, bool) {
	namespace = canonicalNamespace(namespace)
	host, path, ok := splitGitNamespace(namespace)
	if !ok || !isGiteeHost(host) {
		return giteeTarget{}, false
	}
	parts := splitPath(path)
	if len(parts) == 0 || parts[0] == "" {
		return giteeTarget{}, false
	}
	if strings.EqualFold(host, "gitee.com") && len(parts) == 1 {
		return giteeTarget{Host: host, Community: true, Owner: parts[0]}, true
	}
	return giteeTarget{Host: host, Enterprise: parts[0], Group: strings.Join(parts[1:], "/")}, true
}

// canonicalNamespace turns a Gitee enterprise page into the git namespace.
// https://gitee.pjlab.org.cn/L2/_source/groups/L2/agent-trace/trace-collector-repos/repos/
// becomes https://gitee.pjlab.org.cn/L2/agent-trace/trace-collector-repos/repos.
func canonicalNamespace(namespace string) string {
	namespace = strings.TrimRight(strings.TrimSpace(namespace), "/")
	host, path, ok := splitGitNamespace(namespace)
	if !ok || !isGiteeHost(host) {
		return namespace
	}
	parts := giteeGitParts(splitPath(path))
	if len(parts) == 0 {
		return "https://" + host
	}
	return "https://" + host + "/" + strings.Join(parts, "/")
}

func giteeGitParts(parts []string) []string {
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] != "_source" || parts[i+1] != "groups" || i == 0 {
			continue
		}
		enterprise := parts[0]
		rest := append([]string(nil), parts[i+2:]...)
		rest = trimGiteeGroupPage(rest)
		if len(rest) > 0 && rest[0] == enterprise {
			rest = rest[1:]
		}
		return append([]string{enterprise}, rest...)
	}
	return parts
}

// trimGiteeGroupPage drops the trailing page placeholder in a group URL.
// A path segment named repos is a real subgroup and stays in the namespace.
func trimGiteeGroupPage(parts []string) []string {
	if len(parts) > 0 && parts[len(parts)-1] == "-" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func isGiteeHost(host string) bool {
	host = strings.ToLower(host)
	return host == "gitee.com" || strings.HasSuffix(host, ".gitee.com") || strings.HasPrefix(host, "gitee.")
}

func (c giteeClient) api() string {
	if c.API != "" {
		return strings.TrimRight(c.API, "/")
	}
	return "https://gitee.com/api/v5"
}

func (c giteeClient) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (c giteeClient) do(method, path string, form url.Values) (*http.Response, error) {
	var body io.Reader
	contentType := ""
	if form != nil {
		body = strings.NewReader(form.Encode())
		contentType = "application/x-www-form-urlencoded"
	}
	return c.doRaw(method, path, body, contentType)
}

func (c giteeClient) doJSON(method, path string, payload any) (*http.Response, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return c.doRaw(method, path, bytes.NewReader(raw), "application/json")
}

func (c giteeClient) doRaw(method, path string, body io.Reader, contentType string) (*http.Response, error) {
	// Private Gitee accepts the personal token as access_token. A Bearer header
	// on the same host is rejected, so only the community API sends one.
	if c.privateAPI() && c.Token != "" && !strings.Contains(path, "access_token=") {
		if strings.Contains(path, "?") {
			path += "&access_token=" + url.QueryEscape(c.Token)
		} else {
			path += "?access_token=" + url.QueryEscape(c.Token)
		}
	}
	req, err := http.NewRequest(method, c.api()+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "codex-collector")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if !c.privateAPI() && c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return c.http().Do(req)
}

func (c giteeClient) privateAPI() bool {
	return strings.Contains(strings.TrimRight(c.API, "/"), "/api/v8")
}

// ensureGiteeRepo creates owner/name when that repository does not exist.
// owner is the organization or user in enterpriseNamespace, for example
// https://gitee.com/my-group.
func ensureGiteeRepo(c giteeClient, owner, name string) error {
	if c.Token == "" {
		return fmt.Errorf("未配置 GITEE_TOKEN")
	}
	resp, err := c.do(http.MethodGet, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("查询 Gitee 仓库失败: %s", resp.Status)
	}

	orgResp, err := c.do(http.MethodGet, "/orgs/"+url.PathEscape(owner), nil)
	if err != nil {
		return err
	}
	defer orgResp.Body.Close()
	if orgResp.StatusCode == http.StatusOK {
		return c.createRepo("/orgs/"+url.PathEscape(owner)+"/repos", giteeCommunityForm(name))
	}
	if orgResp.StatusCode != http.StatusNotFound {
		msg, _ := io.ReadAll(io.LimitReader(orgResp.Body, 512))
		return fmt.Errorf("查询 Gitee 组织失败: %s %s", orgResp.Status, bytesTrim(msg))
	}

	userResp, err := c.do(http.MethodGet, "/user", nil)
	if err != nil {
		return err
	}
	defer userResp.Body.Close()
	if userResp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(userResp.Body, 512))
		return fmt.Errorf("查询 Gitee 用户失败: %s %s", userResp.Status, bytesTrim(msg))
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(userResp.Body).Decode(&user); err != nil {
		return err
	}
	if user.Login != owner {
		return fmt.Errorf("Gitee 组织 %s 不存在，或当前令牌不能在该组织下创建仓库", owner)
	}
	return c.createRepo("/user/repos", giteeCommunityForm(name))
}

func giteeCommunityForm(name string) url.Values {
	form := url.Values{}
	form.Set("name", name)
	form.Set("private", "true")
	form.Set("auto_init", "false")
	form.Set("description", "trace collector")
	return form
}

func (c giteeClient) createRepo(path string, form url.Values) error {
	resp, err := c.do(http.MethodPost, path, form)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
		return nil
	}
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	text := strings.ToLower(string(msg))
	if resp.StatusCode == http.StatusUnprocessableEntity && (strings.Contains(text, "already") || strings.Contains(string(msg), "已存在")) {
		return nil
	}
	return fmt.Errorf("创建 Gitee 仓库失败: %s %s", resp.Status, bytesTrim(msg))
}

func ensureGiteeEnterpriseRepo(c giteeClient, spec giteeTarget, name string) error {
	if c.Token == "" {
		return fmt.Errorf("未配置 GITEE_TOKEN")
	}
	if spec.Enterprise == "" {
		return fmt.Errorf("Gitee 企业路径为空")
	}
	projectPath := spec.Enterprise + "/" + name
	groupPath := spec.Enterprise
	if spec.Group != "" {
		projectPath = spec.Enterprise + "/" + spec.Group + "/" + name
		groupPath = spec.Enterprise + "/" + spec.Group
	}
	resp, err := c.do(http.MethodGet, "/projects/"+url.PathEscape(projectPath), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	if !giteeRepoMissing(resp.StatusCode, msg) {
		return fmt.Errorf("查询 Gitee 仓库失败: %s %s", resp.Status, bytesTrim(msg))
	}

	groupResp, err := c.do(http.MethodGet, "/groups/"+url.PathEscape(groupPath), nil)
	if err != nil {
		return err
	}
	defer groupResp.Body.Close()
	groupMsg, _ := io.ReadAll(io.LimitReader(groupResp.Body, 512))
	if groupResp.StatusCode != http.StatusOK {
		return fmt.Errorf("查询 Gitee 仓库组失败: %s %s", groupResp.Status, bytesTrim(groupMsg))
	}
	var group struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(groupMsg, &group); err != nil || group.ID == 0 {
		return fmt.Errorf("Gitee 仓库组 %s 没有返回编号", groupPath)
	}
	created, err := c.doJSON(http.MethodPost, "/projects", map[string]any{
		"name":                   name,
		"path":                   name,
		"namespace_id":           group.ID,
		"description":            "trace collector",
		"visibility":             "private",
		"initialize_with_readme": false,
	})
	if err != nil {
		return err
	}
	defer created.Body.Close()
	createdMsg, _ := io.ReadAll(io.LimitReader(created.Body, 512))
	if created.StatusCode == http.StatusOK || created.StatusCode == http.StatusCreated {
		return nil
	}
	text := string(createdMsg)
	if strings.Contains(text, "已存在") || strings.Contains(strings.ToLower(text), "already") {
		return nil
	}
	return fmt.Errorf("创建 Gitee 仓库失败: %s %s", created.Status, bytesTrim(createdMsg))
}

func giteeRepoMissing(status int, body []byte) bool {
	if status == http.StatusNotFound {
		return true
	}
	return status == http.StatusInternalServerError && bytes.Contains(body, []byte("未找到"))
}

func bytesTrim(b []byte) string {
	return strings.TrimSpace(string(b))
}
