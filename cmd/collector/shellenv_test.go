package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTokenLine(t *testing.T) {
	cases := []struct {
		line  string
		key   string
		value string
		ok    bool
	}{
		{`export GITHUB_TOKEN=abc`, "GITHUB_TOKEN", "abc", true},
		{`export GITHUB_TOKEN="quoted"`, "GITHUB_TOKEN", "quoted", true},
		{`GH_TOKEN='single'`, "GH_TOKEN", "single", true},
		{`export CODEUP_TOKEN=plain # note`, "CODEUP_TOKEN", "plain", true},
		{`export GITEE_TOKEN="gitee-token"`, "GITEE_TOKEN", "gitee-token", true},
		{`# export GITHUB_TOKEN=hidden`, "", "", false},
		{`export OTHER=value`, "", "", false},
		{`export GITHUB_TOKEN=$OTHER`, "", "", false},
		{`PATH=/bin`, "", "", false},
	}
	for _, tc := range cases {
		key, value, ok := parseTokenLine(tc.line)
		if ok != tc.ok || key != tc.key || value != tc.value {
			t.Fatalf("%q -> %q %q %v, want %q %q %v", tc.line, key, value, ok, tc.key, tc.value, tc.ok)
		}
	}
}

func TestShellTokenEnvReadsBashrc(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("CODEUP_TOKEN", "")
	t.Setenv("YUNXIAO_TOKEN", "")
	t.Setenv("GITEE_TOKEN", "")
	body := "export PATH=/bin\nexport GITHUB_TOKEN=from-bashrc\nexport GH_TOKEN=from-gh\n"
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte("export GITHUB_TOKEN=from-profile\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pairs := shellTokenEnv()
	if len(pairs) != 2 || pairs[0] != "GITHUB_TOKEN=from-bashrc" || pairs[1] != "GH_TOKEN=from-gh" {
		t.Fatalf("pairs = %#v", pairs)
	}
	if currentTokenStamp() == "" {
		t.Fatal("stamp should change when a shell token is present")
	}
	applyShellTokens()
	if os.Getenv("GITHUB_TOKEN") != "from-bashrc" || os.Getenv("GH_TOKEN") != "from-gh" {
		t.Fatalf("daemon did not load shell tokens: GITHUB_TOKEN=%q GH_TOKEN=%q", os.Getenv("GITHUB_TOKEN"), os.Getenv("GH_TOKEN"))
	}
	t.Setenv("GITHUB_TOKEN", "already")
	t.Setenv("GH_TOKEN", "already")
	if shellTokenEnv() != nil {
		t.Fatalf("existing environment should be left alone, got %#v", shellTokenEnv())
	}
}
