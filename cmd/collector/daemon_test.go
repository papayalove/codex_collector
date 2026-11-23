package main

import "testing"

func TestShouldReplaceDaemonWhenCodexRestarts(t *testing.T) {
	if !shouldReplaceDaemon(10, 20) {
		t.Fatal("a new Codex process should replace the daemon")
	}
	if shouldReplaceDaemon(10, 10) {
		t.Fatal("the same Codex process should keep the daemon")
	}
	if !shouldReplaceDaemon(0, 20) {
		t.Fatal("a daemon started before owner tracking should be replaced")
	}
	if shouldReplaceDaemon(10, 0) {
		t.Fatal("unknown owner should not kill the daemon")
	}
}
