package engine

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCodeupFromNamespace(t *testing.T) {
	spec, ok := codeupFromNamespace("https://codeup.aliyun.com/60d54f3daccf2bbd6659f3ad/codex-collect")
	if !ok || spec.Org != "60d54f3daccf2bbd6659f3ad" || spec.Group != "codex-collect" {
		t.Fatalf("https: %+v %v", spec, ok)
	}
	spec, ok = codeupFromNamespace("git@codeup.aliyun.com:60d54f3daccf2bbd6659f3ad/codex-collect.git")
	if !ok || spec.Org != "60d54f3daccf2bbd6659f3ad" || spec.Group != "codex-collect" {
		t.Fatalf("ssh: %+v %v", spec, ok)
	}
	if _, ok = codeupFromNamespace("https://github.com/alice"); ok {
		t.Fatal("github was treated as codeup")
	}
}

func TestEnsureCodeupRepoCreatesWhenMissing(t *testing.T) {
	var created string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-yunxiao-token") != "test-token" {
			t.Errorf("missing token header")
		}
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/repositories/"):
			http.NotFound(w, r)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/namespaces/"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":9}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/repositories"):
			b, _ := io.ReadAll(r.Body)
			created = string(b)
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	err := ensureCodeupRepo(codeupClient{API: srv.URL, Token: "test-token", HTTP: srv.Client()}, codeupTarget{
		Org:   "60d54f3daccf2bbd6659f3ad",
		Group: "codex-collect",
	}, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(created, `"namespaceId":9`) || !strings.Contains(created, `"path":"abc"`) {
		t.Fatalf("create body: %s", created)
	}
}

func TestEnsureCodeupRepoSkipsWhenPresent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/repositories/") {
			w.WriteHeader(http.StatusOK)
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	err := ensureCodeupRepo(codeupClient{API: srv.URL, Token: "test-token", HTTP: srv.Client()}, codeupTarget{
		Org:   "60d54f3daccf2bbd6659f3ad",
		Group: "codex-collect",
	}, "abc")
	if err != nil {
		t.Fatal(err)
	}
}

func TestEnsureCodeupRepoRequiresToken(t *testing.T) {
	err := ensureCodeupRepo(codeupClient{}, codeupTarget{Org: "60d54f3daccf2bbd6659f3ad"}, "abc")
	if err == nil || !strings.Contains(err.Error(), "CODEUP_TOKEN") {
		t.Fatalf("err = %v", err)
	}
}
