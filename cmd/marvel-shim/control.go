package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
)

// request is one line of JSON on the control socket.
type request struct {
	Cmd    string `json:"cmd"`
	Signal string `json:"signal,omitempty"`
	Data   string `json:"data,omitempty"`
}

type response struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Pid     int    `json:"pid,omitempty"`
	ShimPid int    `json:"shim_pid,omitempty"`
	Running *bool  `json:"running,omitempty"`
	Written int    `json:"written,omitempty"`
	Clients int    `json:"clients,omitempty"`
}

func listenUnix(path string) (net.Listener, error) {
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}

func (s *shim) serveControl(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go s.handleControl(conn)
	}
}

// handleControl serves one supervisor connection until it goes away. A
// disconnect is never fatal to the shim: that is signal 4's "survives
// supervisor restart" property, and it falls out of per-connection handling.
func (s *shim) handleControl(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	enc := json.NewEncoder(conn)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			_ = enc.Encode(response{Error: "bad json: " + err.Error()})
			continue
		}
		_ = enc.Encode(s.dispatch(req))
	}
}

func (s *shim) dispatch(req request) response {
	switch req.Cmd {
	case "status":
		running := true
		select {
		case <-s.exited:
			running = false
		default:
		}
		pid := 0
		if s.cmd.Process != nil {
			pid = s.cmd.Process.Pid
		}
		return response{OK: true, Pid: pid, ShimPid: os.Getpid(), Running: &running, Clients: s.bcast.clientCount()}

	case "signal":
		sig, ok := signalByName(req.Signal)
		if !ok {
			return response{Error: "unknown signal " + req.Signal}
		}
		if err := s.signalChild(sig); err != nil {
			return response{Error: err.Error()}
		}
		return response{OK: true}

	case "stop":
		if err := s.signalChild(syscall.SIGTERM); err != nil {
			return response{Error: err.Error()}
		}
		return response{OK: true}

	case "inject":
		n, err := s.inject(req.Data)
		if err != nil {
			return response{Error: err.Error()}
		}
		return response{OK: true, Written: n}

	default:
		return response{Error: "unknown cmd " + req.Cmd}
	}
}

func signalByName(name string) (syscall.Signal, bool) {
	switch strings.ToUpper(strings.TrimPrefix(strings.ToUpper(name), "SIG")) {
	case "HUP":
		return syscall.SIGHUP, true
	case "INT":
		return syscall.SIGINT, true
	case "TERM":
		return syscall.SIGTERM, true
	case "KILL":
		return syscall.SIGKILL, true
	case "USR1":
		return syscall.SIGUSR1, true
	case "USR2":
		return syscall.SIGUSR2, true
	case "WINCH":
		return syscall.SIGWINCH, true
	}
	return 0, false
}
