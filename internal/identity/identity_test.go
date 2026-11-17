package identity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveEmailFirst(t *testing.T) {
	id, source := Resolve("User@Example.com", "acct-1", "device-1")
	if source != "email" {
		t.Fatalf("source %s", source)
	}
	again, _ := Resolve(" user@example.com ", "acct-1", "device-1")
	if id != again {
		t.Fatalf("email hash not stable: %s vs %s", id, again)
	}
	if len(id) != 64 {
		t.Fatalf("sha256 hex length %d", len(id))
	}
}

func TestResolveAccountThenDevice(t *testing.T) {
	id, source := Resolve("", "acct-1", "device-1")
	if source != "account" || id != "acct-1" {
		t.Fatalf("got %s %s", source, id)
	}
	id, source = Resolve("", "", "device-1")
	if source != "device" || id != "device-1" {
		t.Fatalf("got %s %s", source, id)
	}
}

func TestFirstMachineID(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	empty := filepath.Join(dir, "empty")
	spaced := filepath.Join(dir, "spaced")
	good := filepath.Join(dir, "id")
	if err := os.WriteFile(empty, []byte(" \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spaced, []byte("aa bb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(good, []byte("0123456789abcdef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := firstMachineID(missing, empty, spaced, good); got != "0123456789abcdef" {
		t.Fatalf("got %q", got)
	}
	if got := firstMachineID(missing, empty); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestRejectTokenAsAccount(t *testing.T) {
	id, source := Resolve("", "sk-secret-value", "device-1")
	if source != "device" || id != "device-1" {
		t.Fatalf("token was accepted: %s %s", source, id)
	}
}
