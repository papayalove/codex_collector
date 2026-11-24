package engine

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGiteeOwner(t *testing.T) {
	owner, ok := giteeOwner("https://gitee.com/my-group")
	if !ok || owner != "my-group" {
		t.Fatalf("https owner: %q %v", owner, ok)
	}
	owner, ok = giteeOwner("git@gitee.com:my-group/extra.git")
	if !ok || owner != "my-group" {
		t.Fatalf("ssh owner: %q %v", owner, ok)
	}
	if _, ok = giteeOwner("https://github.com/alice"); ok {
		t.Fatal("github was treated as gitee")
	}
}

func TestEnsureGiteeRepoCreatesUnderOrg(t *testing.T) {
	var created string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/my-group/abc":
			http.NotFound(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/orgs/my-group":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/orgs/my-group/repos":
			b, _ := io.ReadAll(r.Body)
			created = string(b)
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	err := ensureGiteeRepo(giteeClient{API: srv.URL, Token: "test-token", HTTP: srv.Client()}, "my-group", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(created, "name=abc") || !strings.Contains(created, "private=true") || !strings.Contains(created, "auto_init=false") {
		t.Fatalf("create body: %s", created)
	}
}

func TestEnsureGiteeRepoCreatesForUser(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/alice/abc":
			http.NotFound(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/orgs/alice":
			http.NotFound(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/user":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"login":"alice"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/user/repos":
			path = r.URL.Path
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	err := ensureGiteeRepo(giteeClient{API: srv.URL, Token: "test-token", HTTP: srv.Client()}, "alice", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/user/repos" {
		t.Fatalf("create path = %s", path)
	}
}

func TestEnsureGiteeRepoRejectsUnknownOrg(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/other/abc":
			http.NotFound(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/orgs/other":
			http.NotFound(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/user":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"login":"alice"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	err := ensureGiteeRepo(giteeClient{API: srv.URL, Token: "test-token", HTTP: srv.Client()}, "other", "abc")
	if err == nil || !strings.Contains(err.Error(), "other") {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureGiteeRepoSkipsWhenPresent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/repos/my-group/abc" {
			w.WriteHeader(http.StatusOK)
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	err := ensureGiteeRepo(giteeClient{API: srv.URL, Token: "test-token", HTTP: srv.Client()}, "my-group", "abc")
	if err != nil {
		t.Fatal(err)
	}
}

func TestEnsureGiteeRepoRequiresToken(t *testing.T) {
	err := ensureGiteeRepo(giteeClient{}, "my-group", "abc")
	if err == nil || !strings.Contains(err.Error(), "GITEE_TOKEN") {
		t.Fatalf("err = %v", err)
	}
}

func TestPJLabNamespaceBecomesGitRoot(t *testing.T) {
	page := "https://gitee.pjlab.org.cn/L2/_source/groups/L2/agent-trace/trace-collector-repos/repos/"
	want := "https://gitee.pjlab.org.cn/L2/agent-trace/trace-collector-repos/repos"
	if got := canonicalNamespace(page); got != want {
		t.Fatalf("canonical = %s", got)
	}
	withDash := "https://gitee.pjlab.org.cn/L2/_source/groups/L2/agent-trace/trace-collector-repos/repos/-/"
	if got := canonicalNamespace(withDash); got != want {
		t.Fatalf("dash canonical = %s", got)
	}
	if got := remoteURL(page, "abc"); got != want+"/abc.git" {
		t.Fatalf("remote = %s", got)
	}
	spec, ok := giteeFromNamespace(page)
	if !ok || spec.Community || spec.Enterprise != "L2" || spec.Group != "agent-trace/trace-collector-repos/repos" {
		t.Fatalf("spec = %+v %v", spec, ok)
	}
}

func TestEnsureGiteeEnterpriseRepoCreatesInGroup(t *testing.T) {
	var created, lookup, group string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		if r.URL.Query().Get("access_token") != "test-token" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v8/projects/"):
			lookup = r.URL.EscapedPath()
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"未找到关联的相关仓库的信息"}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v8/groups/"):
			group = r.URL.EscapedPath()
			_, _ = w.Write([]byte(`{"id":687,"full_path":"agent-trace/trace-collector-repos/repos"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v8/projects":
			b, _ := io.ReadAll(r.Body)
			created = string(b)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":2424}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.RequestURI())
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	spec := giteeTarget{Host: "gitee.pjlab.org.cn", Enterprise: "L2", Group: "agent-trace/trace-collector-repos/repos"}
	err := ensureGiteeEnterpriseRepo(giteeClient{API: srv.URL + "/api/v8", Token: "test-token", HTTP: srv.Client()}, spec, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if lookup != "/api/v8/projects/"+url.PathEscape("L2/agent-trace/trace-collector-repos/repos/abc") {
		t.Fatalf("lookup = %s", lookup)
	}
	if group != "/api/v8/groups/"+url.PathEscape("L2/agent-trace/trace-collector-repos/repos") {
		t.Fatalf("group = %s", group)
	}
	if !strings.Contains(created, `"name":"abc"`) || !strings.Contains(created, `"path":"abc"`) || !strings.Contains(created, `"namespace_id":687`) || !strings.Contains(created, `"visibility":"private"`) || !strings.Contains(created, `"initialize_with_readme":false`) {
		t.Fatalf("create body: %s", created)
	}
}

func TestGitAuthArgsUsesTokenForGitee(t *testing.T) {
	t.Setenv("GITEE_TOKEN", "gitee_testtokenvalue")
	args := gitAuthArgs("https://gitee.com/my-group/repo.git")
	got := strings.Join(args, "\n")
	if !strings.Contains(got, "credential.helper=") || !strings.Contains(got, "@gitee.com/.insteadOf=https://gitee.com/") || !strings.Contains(got, "gitee_testtokenvalue") {
		t.Fatalf("args = %q", args)
	}
	lab := strings.Join(gitAuthArgs("https://gitee.pjlab.org.cn/L2/agent-trace/trace-collector-repos/abc.git"), "\n")
	if !strings.Contains(lab, "@gitee.pjlab.org.cn/.insteadOf=https://gitee.pjlab.org.cn/") {
		t.Fatalf("enterprise args = %q", lab)
	}
	t.Setenv("GITEE_TOKEN", "")
	if args := gitAuthArgs("https://gitee.com/my-group/repo.git"); args != nil {
		t.Fatalf("missing token args = %q", args)
	}
}
