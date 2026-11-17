package engine

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnsureGitHubRepoCreatesWhenMissing(t *testing.T) {
	var created bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/alice/abc":
			http.NotFound(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/users/alice":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"type":"User"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/user/repos":
			created = true
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	err := ensureGitHubRepo(githubClient{API: srv.URL, Token: "test-token", HTTP: srv.Client()}, "alice", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected create request")
	}
}

func TestEnsureGitHubRepoSkipsWhenPresent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/repos/alice/abc" {
			w.WriteHeader(http.StatusOK)
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	err := ensureGitHubRepo(githubClient{API: srv.URL, Token: "test-token", HTTP: srv.Client()}, "alice", "abc")
	if err != nil {
		t.Fatal(err)
	}
}

func TestGitHubOwner(t *testing.T) {
	owner, ok := githubOwner("https://github.com/alice")
	if !ok || owner != "alice" {
		t.Fatalf("https owner: %q %v", owner, ok)
	}
	owner, ok = githubOwner("git@github.com:alice")
	if !ok || owner != "alice" {
		t.Fatalf("ssh owner: %q %v", owner, ok)
	}
}
