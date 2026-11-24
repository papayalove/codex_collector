//go:build !windows

package main

import (
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// collectorOwnerPID is the host agent process (Codex or Claude Code) for this
// hook. It stays the same while that agent is open, and changes after the agent
// is started again.
func collectorOwnerPID() int {
	pid := os.Getpid()
	fallback := os.Getppid()
	for i := 0; i < 8; i++ {
		parent := parentPID(pid)
		if parent <= 0 || parent == pid {
			break
		}
		if isAgentProcessName(processName(parent)) {
			return parent
		}
		pid = parent
	}
	if fallback > 1 {
		return fallback
	}
	return 0
}

func parentPID(pid int) int {
	out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return n
}

func processName(pid int) string {
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil
}

func killProcess(pid int) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = proc.Kill()
}

func listenerPID(addr string) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return 0
	}
	out, err := exec.Command("lsof", "-nP", "-iTCP:"+port, "-sTCP:LISTEN", "-t").Output()
	if err != nil {
		return 0
	}
	for _, line := range strings.Fields(string(out)) {
		pid, _ := strconv.Atoi(line)
		if pid > 0 {
			return pid
		}
	}
	return 0
}

func lockFile(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

func unlockFile(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
