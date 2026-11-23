//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// detach starts the daemon in its own session and drops the hook's stdio.
// Otherwise the daemon stays in the hook's process group and exits with it,
// so later commits and pushes never run.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	f, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return
	}
	cmd.Stdin = f
	cmd.Stdout = f
	cmd.Stderr = f
	if tty := consoleWriter(); tty != nil {
		cmd.Stderr = tty
	}
}
