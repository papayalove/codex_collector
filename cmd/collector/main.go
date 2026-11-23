package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"codex-collector/internal/engine"
)

func main() {
	if len(os.Args) < 2 {
		os.Exit(0)
	}
	switch os.Args[1] {
	case "daemon":
		runDaemon()
	case "hook":
		runHook()
	default:
		os.Exit(0)
	}
}

func configDir() string {
	if v := os.Getenv("CODEX_COLLECTOR_DATA"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex-collector")
}

func dataDir() string {
	base := configDir()
	return engine.EffectiveDataDir(base, loadCfg(base))
}

func withDaemonLock(fn func()) {
	dir := dataDir()
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(filepath.Join(dir, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		fn()
		return
	}
	defer f.Close()
	lockFile(f)
	defer unlockFile(f)
	fn()
}

func runHook() {
	body, _ := io.ReadAll(os.Stdin)
	if len(bytes.TrimSpace(body)) == 0 {
		return
	}
	withDaemonLock(func() { ensureCurrentDaemon() })
	if err := call(body); err != nil {
		withDaemonLock(func() {
			owner := collectorOwnerPID()
			info, loadErr := loadDaemonInfo()
			if loadErr != nil || info.OwnerPid != owner || !processAlive(info.Pid) {
				_ = startDaemon(owner)
			}
		})
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if call(body) == nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

type daemonInfo struct {
	Addr       string `json:"addr"`
	Token      string `json:"token"`
	Pid        int    `json:"pid"`
	OwnerPid   int    `json:"ownerPid"`
	TokenStamp string `json:"tokenStamp,omitempty"`
}

// ensureCurrentDaemon starts the collector that belongs to this Codex process.
// The daemon is detached, so closing Codex leaves the previous process running.
// The next hook after Codex starts again replaces that process. It is also
// replaced once when a shell token was not passed to the running process.
func ensureCurrentDaemon() {
	owner := collectorOwnerPID()
	if owner <= 0 {
		return
	}
	info, err := loadDaemonInfo()
	running := err == nil && info.Pid > 0 && processAlive(info.Pid)
	if running && !shouldReplaceDaemon(info.OwnerPid, owner) && info.TokenStamp == currentTokenStamp() {
		return
	}
	if err == nil {
		pid := info.Pid
		if pid <= 0 {
			pid = listenerPID(info.Addr)
		}
		stopCollector(pid)
	}
	_ = os.Remove(filepath.Join(dataDir(), "daemon.json"))
	if err := startDaemon(owner); err != nil {
		return
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if daemonReachable() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func shouldReplaceDaemon(storedOwner, currentOwner int) bool {
	if currentOwner <= 0 {
		return false
	}
	return storedOwner != currentOwner
}

func daemonReachable() bool {
	addr, _, err := readDaemonFile()
	if err != nil {
		return false
	}
	conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func stopCollector(pid int) {
	if pid <= 0 || pid == os.Getpid() {
		return
	}
	if !strings.Contains(strings.ToLower(processName(pid)), "collector") {
		return
	}
	killProcess(pid)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && processAlive(pid) {
		time.Sleep(50 * time.Millisecond)
	}
}

func call(body []byte) error {
	addr, token, err := readDaemonFile()
	if err != nil {
		return err
	}
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(55 * time.Second))
	req, _ := json.Marshal(map[string]any{"token": token, "event": json.RawMessage(body)})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return err
	}
	_, err = bufio.NewReader(conn).ReadBytes('\n')
	if err == io.EOF {
		return nil
	}
	return err
}

func loadDaemonInfo() (daemonInfo, error) {
	b, err := os.ReadFile(filepath.Join(dataDir(), "daemon.json"))
	if err != nil {
		return daemonInfo{}, err
	}
	var info daemonInfo
	if err := json.Unmarshal(b, &info); err != nil {
		return daemonInfo{}, err
	}
	return info, nil
}

func readDaemonFile() (string, string, error) {
	info, err := loadDaemonInfo()
	if err != nil {
		return "", "", err
	}
	return info.Addr, info.Token, nil
}

func startDaemon(owner int) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "daemon", strconv.Itoa(owner), currentTokenStamp())
	cmd.Env = daemonEnviron()
	detach(cmd)
	return cmd.Start()
}

func runDaemon() {
	ignoreDaemonSignals()
	applyShellTokens()
	base := configDir()
	dir := dataDir()
	_ = os.MkdirAll(base, 0o755)
	_ = os.MkdirAll(dir, 0o755)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return
	}
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	token := hex.EncodeToString(buf)
	owner := 0
	if len(os.Args) >= 3 {
		owner, _ = strconv.Atoi(os.Args[2])
	}
	stamp := ""
	if len(os.Args) >= 4 {
		stamp = os.Args[3]
	}
	raw, _ := json.Marshal(daemonInfo{Addr: ln.Addr().String(), Token: token, Pid: os.Getpid(), OwnerPid: owner, TokenStamp: stamp})
	_ = os.WriteFile(filepath.Join(dir, "daemon.json"), raw, 0o600)
	eng := &engine.Engine{DataDir: dir, ConfigDir: base, Log: os.Stderr}
	eng.ClearStalePush()
	eng.AfterPush = func() { eng.CommitAll(loadCfg(base)) }
	go func() {
		for {
			eng.CommitAll(loadCfg(base))
			time.Sleep(2 * time.Second)
		}
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go serve(conn, token, eng)
	}
}

func loadCfg(dir string) engine.Config {
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return engine.Config{}
	}
	var cfg engine.Config
	_ = json.Unmarshal(b, &cfg)
	return cfg
}

func serve(conn net.Conn, token string, eng *engine.Engine) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(55 * time.Second))
	body, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(body) == 0 {
		return
	}
	var req struct {
		Token string          `json:"token"`
		Event json.RawMessage `json:"event"`
	}
	if json.Unmarshal(body, &req) != nil || req.Token != token {
		return
	}
	result := eng.Handle(req.Event)
	resp, _ := json.Marshal(result)
	_, _ = conn.Write(append(resp, '\n'))
	cfgDir := eng.ConfigDir
	if cfgDir == "" {
		cfgDir = eng.DataDir
	}
	go eng.CommitAll(loadCfg(cfgDir))
}
