//go:build windows

package main

import (
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procCreateToolhelp32Snapshot = kernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW          = kernel32.NewProc("Process32FirstW")
	procProcess32NextW           = kernel32.NewProc("Process32NextW")
	procLockFileEx               = kernel32.NewProc("LockFileEx")
	procUnlockFileEx             = kernel32.NewProc("UnlockFileEx")
)

const (
	th32csSnapProcess       = 0x00000002
	lockfileExclusiveLock   = 0x00000002
	processQueryLimitedInfo = 0x1000
)

type processEntry32 struct {
	Size            uint32
	CntUsage        uint32
	ProcessID       uint32
	DefaultHeapID   uintptr
	ModuleID        uint32
	Threads         uint32
	ParentProcessID uint32
	PriClassBase    int32
	Flags           uint32
	ExeFile         [syscall.MAX_PATH]uint16
}

type processRow struct {
	parent int
	name   string
}

func processTable() map[int]processRow {
	snap, _, _ := procCreateToolhelp32Snapshot.Call(th32csSnapProcess, 0)
	if snap == uintptr(syscall.InvalidHandle) {
		return nil
	}
	defer syscall.CloseHandle(syscall.Handle(snap))
	var entry processEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	ok, _, _ := procProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&entry)))
	table := map[int]processRow{}
	for ok != 0 {
		table[int(entry.ProcessID)] = processRow{
			parent: int(entry.ParentProcessID),
			name:   syscall.UTF16ToString(entry.ExeFile[:]),
		}
		entry.Size = uint32(unsafe.Sizeof(entry))
		ok, _, _ = procProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&entry)))
	}
	return table
}

// collectorOwnerPID is the host agent process (Codex or Claude Code) for this
// hook. It stays the same while that agent is open, and changes after the agent
// is started again.
func collectorOwnerPID() int {
	table := processTable()
	pid := os.Getpid()
	fallback := os.Getppid()
	for i := 0; i < 8; i++ {
		row, ok := table[pid]
		if !ok || row.parent <= 0 || row.parent == pid {
			break
		}
		parent, ok := table[row.parent]
		if ok && isAgentProcessName(parent.name) {
			return row.parent
		}
		pid = row.parent
	}
	if fallback > 1 {
		return fallback
	}
	return 0
}

func processName(pid int) string {
	row, ok := processTable()[pid]
	if !ok {
		return ""
	}
	return row.name
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(processQueryLimitedInfo, false, uint32(pid))
	if err != nil {
		return false
	}
	syscall.CloseHandle(h)
	return true
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
	out, err := exec.Command("netstat", "-ano", "-p", "tcp").Output()
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || !strings.EqualFold(fields[3], "LISTENING") {
			continue
		}
		if !strings.HasSuffix(fields[1], ":"+port) {
			continue
		}
		pid, _ := strconv.Atoi(fields[len(fields)-1])
		return pid
	}
	return 0
}

func lockFile(f *os.File) {
	var ol syscall.Overlapped
	_, _, _ = procLockFileEx.Call(f.Fd(), lockfileExclusiveLock, 0, 1, 0, uintptr(unsafe.Pointer(&ol)))
}

func unlockFile(f *os.File) {
	var ol syscall.Overlapped
	_, _, _ = procUnlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&ol)))
}
