//go:build !windows

package main

import (
	"os/signal"
	"syscall"
)

func ignoreDaemonSignals() {
	signal.Ignore(syscall.SIGHUP)
}
