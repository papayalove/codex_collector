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

func runHook() {
	body, _ := io.ReadAll(os.Stdin)
	if len(bytes.TrimSpace(body)) == 0 {
		return
	}
	if err := call(body); err != nil {
		_ = startDaemon()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if call(body) == nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
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

func readDaemonFile() (string, string, error) {
	b, err := os.ReadFile(filepath.Join(dataDir(), "daemon.json"))
	if err != nil {
		return "", "", err
	}
	var info struct {
		Addr  string `json:"addr"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(b, &info); err != nil {
		return "", "", err
	}
	return info.Addr, info.Token, nil
}

func startDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "daemon")
	cmd.Env = os.Environ()
	detach(cmd)
	return cmd.Start()
}

func runDaemon() {
	ignoreDaemonSignals()
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
	info, _ := json.Marshal(map[string]string{"addr": ln.Addr().String(), "token": token})
	_ = os.WriteFile(filepath.Join(dir, "daemon.json"), info, 0o600)
	eng := &engine.Engine{DataDir: dir, ConfigDir: base}
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
