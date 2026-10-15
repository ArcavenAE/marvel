package main

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemon"
)

// scriptedDaemon answers each request with reply(req) and records what it was
// sent. The socket is found the way a real client finds it, through the
// environment, under a scratch HOME.
func scriptedDaemon(t *testing.T, reply func(daemon.Request) daemon.Response) func() []daemon.Request {
	t.Helper()
	home, err := os.MkdirTemp("", "mg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	oldSocket, oldCluster, oldGiven := socketPath, clusterName, clusterFlagGiven
	socketPath, clusterName, clusterFlagGiven = "", "", false
	t.Cleanup(func() { socketPath, clusterName, clusterFlagGiven = oldSocket, oldCluster, oldGiven })
	sock := filepath.Join(home, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var (
		mu   sync.Mutex
		got  []daemon.Request
		wg   sync.WaitGroup
		done atomic.Bool
	)
	t.Cleanup(func() { done.Store(true); _ = ln.Close(); wg.Wait() })
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var req daemon.Request
			resp := daemon.Response{}
			if err := json.NewDecoder(c).Decode(&req); err == nil {
				mu.Lock()
				got = append(got, req)
				mu.Unlock()
				resp = reply(req)
			}
			_ = json.NewEncoder(c).Encode(resp)
			_ = c.Close()
		}
	}()
	t.Setenv(config.SocketEnv, sock)
	return func() []daemon.Request {
		mu.Lock()
		defer mu.Unlock()
		return append([]daemon.Request(nil), got...)
	}
}

func runOK(daemon.Request) daemon.Response {
	r, _ := json.Marshal(map[string]string{"status": "created", "session_key": "default/n"})
	return daemon.Response{Result: r}
}

func execRoot(t *testing.T, args ...string) error {
	t.Helper()
	root := newRootCmd()
	root.SetArgs(args)
	root.SilenceUsage, root.SilenceErrors = true, true
	var err error
	captureStdout(t, func() { err = root.Execute() })
	return err
}

func TestRunSendsModePromptEnvNameAndPassthrough(t *testing.T) {
	sent := scriptedDaemon(t, runOK)
	pf := filepath.Join(t.TempDir(), "prompt.md")
	if err := os.WriteFile(pf, []byte("do the thing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := execRoot(t, "run", "--experimental", "--mode", "headless", "--prompt-file", pf,
		"--env", "A=1", "--env", "B=two=2", "--name", "branch-7", "--workspace", "w",
		"--", "claude", "--agent", "orch", "--model", "m")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	reqs := sent()
	if len(reqs) != 1 || reqs[0].Method != "run" {
		t.Fatalf("requests = %+v, want one run", reqs)
	}
	var p struct {
		Mode, Prompt, Name, RuntimeCommand string
		Env                                map[string]string
		RuntimeArgs                        []string `json:"runtime_args"`
	}
	raw := reqs[0].Params
	var m map[string]json.RawMessage
	_ = json.Unmarshal(raw, &m)
	for k, dst := range map[string]any{
		"mode": &p.Mode, "prompt": &p.Prompt, "name": &p.Name,
		"runtime_command": &p.RuntimeCommand, "env": &p.Env, "runtime_args": &p.RuntimeArgs,
	} {
		if err := json.Unmarshal(m[k], dst); err != nil {
			t.Fatalf("param %s: %v (%s)", k, err, raw)
		}
	}
	if p.Mode != "headless" || p.Prompt != "do the thing\n" || p.Name != "branch-7" {
		t.Errorf("mode %q prompt %q name %q", p.Mode, p.Prompt, p.Name)
	}
	if p.Env["A"] != "1" || p.Env["B"] != "two=2" || len(p.Env) != 2 {
		t.Errorf("env = %v, want A=1 and B=two=2", p.Env)
	}
	if p.RuntimeCommand != "claude" || strings.Join(p.RuntimeArgs, " ") != "--agent orch --model m" {
		t.Errorf("runtime %q %v, want claude with the args after --", p.RuntimeCommand, p.RuntimeArgs)
	}
}

// Every new flag is refused without --experimental, and nothing reaches the
// daemon.
func TestRunNewFlagsAreRefusedWithoutExperimental(t *testing.T) {
	pf := filepath.Join(t.TempDir(), "p.md")
	if err := os.WriteFile(pf, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, flags := range [][]string{
		{"--mode", "headless"}, {"--prompt-file", pf}, {"--env", "A=1"}, {"--name", "n"},
	} {
		sent := scriptedDaemon(t, runOK)
		err := execRoot(t, append(append([]string{"run"}, flags...), "sleep", "1")...)
		if err == nil || !strings.Contains(err.Error(), "experimental") || !strings.Contains(err.Error(), flags[0]) {
			t.Errorf("%v: error = %v, want a refusal naming the flag and --experimental", flags, err)
		}
		if n := len(sent()); n != 0 {
			t.Errorf("%v: %d requests reached the daemon, want 0", flags, n)
		}
	}
}

func TestRunRefusesAMalformedEnvAndAMissingPromptFile(t *testing.T) {
	sent := scriptedDaemon(t, runOK)
	if err := execRoot(t, "run", "--experimental", "--env", "NOEQUALS", "sleep", "1"); err == nil || !strings.Contains(err.Error(), "KEY=VALUE") {
		t.Errorf("malformed env: error = %v, want a KEY=VALUE refusal", err)
	}
	if err := execRoot(t, "run", "--experimental", "--prompt-file", filepath.Join(t.TempDir(), "gone"), "sleep", "1"); err == nil {
		t.Error("a missing prompt file was accepted")
	}
	if n := len(sent()); n != 0 {
		t.Errorf("%d requests reached the daemon, want 0", n)
	}
}

// A plain run is unchanged and needs no flag.
func TestPlainRunNeedsNoExperimentalFlag(t *testing.T) {
	sent := scriptedDaemon(t, runOK)
	if err := execRoot(t, "run", "sleep", "1"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if n := len(sent()); n != 1 {
		t.Errorf("requests = %d, want 1", n)
	}
}

func describing(states ...string) func(daemon.Request) daemon.Response {
	var i atomic.Int32
	return func(req daemon.Request) daemon.Response {
		n := int(i.Add(1)) - 1
		if n >= len(states) {
			n = len(states) - 1
		}
		if states[n] == "unknown" {
			return daemon.Response{Error: "session default/n: resource not found"}
		}
		r, _ := json.Marshal(map[string]any{"Name": "n", "State": states[n]})
		return daemon.Response{Result: r}
	}
}

func waitCode(t *testing.T, args ...string) int {
	t.Helper()
	err := execRoot(t, append([]string{"wait"}, args...)...)
	if err == nil {
		return 0
	}
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	t.Fatalf("wait error is not an exit code: %v", err)
	return -1
}

func TestWaitExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		states []string
		want   int
	}{
		{"succeeded", []string{"succeeded"}, 0},
		{"failed", []string{"failed"}, 1},
		{"crashed", []string{"crashed"}, 2},
		{"still running at the timeout", []string{"running"}, 3},
		{"unknown key", []string{"unknown"}, 4},
		{"pending then running then succeeded", []string{"pending", "running", "succeeded"}, 0},
		{"crashloop backoff keeps polling", []string{"crashloop-backoff", "failed"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scriptedDaemon(t, describing(tc.states...))
			got := waitCode(t, "--experimental", "default/n", "--timeout", "400ms", "--poll", "10ms")
			if got != tc.want {
				t.Errorf("exit code = %d, want %d", got, tc.want)
			}
		})
	}
}

// wait reads the store through describe, once per poll, and stops asking when
// the state is terminal.
func TestWaitReadsStoreStateAndStopsWhenTerminal(t *testing.T) {
	sent := scriptedDaemon(t, describing("running", "running", "succeeded", "failed"))
	start := time.Now()
	if got := waitCode(t, "--experimental", "default/n", "--timeout", "2s", "--poll", "20ms"); got != 0 {
		t.Fatalf("exit code = %d, want 0", got)
	}
	reqs := sent()
	if len(reqs) != 3 {
		t.Fatalf("requests = %d, want 3 (it stops at the first terminal state)", len(reqs))
	}
	for _, r := range reqs {
		if r.Method != "describe" || !strings.Contains(string(r.Params), `"session"`) {
			t.Errorf("request %s %s, want describe of a session", r.Method, r.Params)
		}
	}
	if time.Since(start) > time.Second {
		t.Errorf("took %v for two polls", time.Since(start))
	}
}

func TestWaitIsRefusedWithoutExperimental(t *testing.T) {
	sent := scriptedDaemon(t, describing("succeeded"))
	err := execRoot(t, "wait", "default/n")
	if err == nil || !strings.Contains(err.Error(), "experimental") {
		t.Fatalf("error = %v, want a refusal naming --experimental", err)
	}
	var ee *exitError
	if errors.As(err, &ee) {
		t.Errorf("a refusal must not look like a wait result, got exit code %d", ee.Code)
	}
	if n := len(sent()); n != 0 {
		t.Errorf("%d requests reached the daemon, want 0", n)
	}
}

// A daemon the client cannot reach, or one that answers with an error, says
// nothing about the session. wait must not report that as exit 1, which means
// the session failed: a caller that retries a failed session would start a
// second one while the first is still running. It gets its own code, 5.
func TestWaitReportsAnUnreachableDaemonAsExit5NotFailed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldSocket, oldCluster, oldGiven := socketPath, clusterName, clusterFlagGiven
	socketPath, clusterName, clusterFlagGiven = "", "", false
	t.Cleanup(func() { socketPath, clusterName, clusterFlagGiven = oldSocket, oldCluster, oldGiven })
	t.Setenv(config.SocketEnv, filepath.Join(home, "none.sock"))
	err := execRoot(t, "wait", "--experimental", "default/n", "--timeout", "200ms", "--poll", "10ms")
	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("error = %v, want an exit code", err)
	}
	if ee.Code != waitDaemonError {
		t.Errorf("exit code = %d, want %d", ee.Code, waitDaemonError)
	}
}

func TestWaitReportsADaemonErrorAsExit5NotFailed(t *testing.T) {
	scriptedDaemon(t, func(daemon.Request) daemon.Response {
		return daemon.Response{Error: "bolt: database not open"}
	})
	if got := waitCode(t, "--experimental", "default/n", "--timeout", "200ms", "--poll", "10ms"); got != waitDaemonError {
		t.Errorf("exit code = %d, want %d", got, waitDaemonError)
	}
}

// The five codes are distinct, and exit 1 is only for a session that failed.
func TestWaitExitCodesAreDistinct(t *testing.T) {
	seen := map[int]bool{}
	for _, c := range []int{waitSucceeded, waitFailed, waitCrashed, waitTimeout, waitUnknown, waitDaemonError} {
		if seen[c] {
			t.Errorf("exit code %d is used twice", c)
		}
		seen[c] = true
	}
	if waitDaemonError != 5 {
		t.Errorf("waitDaemonError = %d, want 5", waitDaemonError)
	}
}
