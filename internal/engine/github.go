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

type githubClient struct {
	API   string
	Token string
	HTTP  *http.Client
}

func githubToken() string {
	if v := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("GH_TOKEN"))
}

// gitAuthArgs supplies the GitHub token to this push only.
// Creating the remote uses the token on the HTTP API. git push does not read
// that variable, and the collector has no window to ask for a password.
// An Authorization extra header is not enough: Git still asks for a username
// and then exits because the terminal prompt is disabled. Rewriting the URL
// for this command only puts the token in the request. The saved remote and
// other GitHub accounts on the machine are left unchanged.
func gitAuthArgs(remote string) []string {
	if _, ok := githubOwner(remote); ok {
		token := githubToken()
		if token == "" {
			return nil
		}
		user := url.UserPassword("x-access-token", token).String()
		return []string{
			"-c", "credential.helper=",
			"-c", "url.https://" + user + "@github.com/.insteadOf=https://github.com/",
		}
	}
	if host, path, ok := splitGitNamespace(remote); ok && isGiteeHost(host) && len(splitPath(path)) > 0 {
		token := giteeToken()
		if token == "" {
			return nil
		}
		// Gitee accepts the private token as both the username and the password.
		user := url.UserPassword(token, token).String()
		return []string{
			"-c", "credential.helper=",
			"-c", "url.https://" + user + "@" + host + "/.insteadOf=https://" + host + "/",
		}
	}
	return nil
}

func githubOwner(namespace string) (string, bool) {
	s := strings.TrimSpace(namespace)
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimRight(s, "/")
	var rest string
	switch {
	case strings.HasPrefix(s, "https://github.com/"):
		rest = strings.TrimPrefix(s, "https://github.com/")
	case strings.HasPrefix(s, "http://github.com/"):
		rest = strings.TrimPrefix(s, "http://github.com/")
	case strings.HasPrefix(s, "git@github.com:"):
		rest = strings.TrimPrefix(s, "git@github.com:")
	default:
		return "", false
	}
	owner := strings.Split(rest, "/")[0]
	if owner == "" {
		return "", false
	}
	return owner, true
}

func (c githubClient) api() string {
	if c.API != "" {
		return strings.TrimRight(c.API, "/")
	}
	return "https://api.github.com"
}

func (c githubClient) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (c githubClient) do(method, path string, body any) (*http.Response, error) {
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
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "codex-collector")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return c.http().Do(req)
}

// ensureGitHubRepo creates owner/name when that repository does not exist.
func ensureGitHubRepo(c githubClient, owner, name string) error {
	if c.Token == "" {
		return fmt.Errorf("未配置 GITHUB_TOKEN")
	}
	resp, err := c.do(http.MethodGet, "/repos/"+owner+"/"+name, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("查询 GitHub 仓库失败: %s", resp.Status)
	}

	userResp, err := c.do(http.MethodGet, "/users/"+owner, nil)
	if err != nil {
		return err
	}
	defer userResp.Body.Close()
	if userResp.StatusCode != http.StatusOK {
		return fmt.Errorf("查询 GitHub 用户失败: %s", userResp.Status)
	}
	var account struct {
		Type string `json:"type"`
	}
	if err := json.NewDecoder(userResp.Body).Decode(&account); err != nil {
		return err
	}
	path := "/user/repos"
	if account.Type == "Organization" {
		path = "/orgs/" + owner + "/repos"
	}
	create, err := c.do(http.MethodPost, path, map[string]any{
		"name":        name,
		"private":     true,
		"auto_init":   false,
		"description": "codex collector",
	})
	if err != nil {
		return err
	}
	defer create.Body.Close()
	if create.StatusCode == http.StatusCreated || create.StatusCode == http.StatusUnprocessableEntity {
		return nil
	}
	msg, _ := io.ReadAll(io.LimitReader(create.Body, 512))
	return fmt.Errorf("创建 GitHub 仓库失败: %s %s", create.Status, bytes.TrimSpace(msg))
}
