package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/knownhosts"
	"github.com/arcavenae/marvel/internal/paths"
)

// The F rig for the fan-out: a JSON-RPC responder on a unix socket in a short
// temp directory, in one of the modes a cluster can be in.
type fakeMode int

const (
	fakeAnswers     fakeMode = iota // answers get with rows
	fakeStarting                    // answers with Starting set
	fakeStartingErr                 // refuses with Starting set and an error
	fakeDaemonError                 // answers with Response.Error
	fakeBadReply                    // answers with a result that does not parse
	fakeHung                        // reads the request and never answers
)

type fakeCluster struct {
	sock string
	mu   sync.Mutex
	got  []string
	n    int
}

func (f *fakeCluster) methods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.got...)
}

func (f *fakeCluster) conns() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n
}

func startFake(t *testing.T, mode fakeMode, rows []api.Session) *fakeCluster {
	t.Helper()
	dir, err := os.MkdirTemp("", "fo")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f := &fakeCluster{sock: filepath.Join(dir, "d.sock")}
	ln, err := net.Listen("unix", f.sock)
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop); _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.n++
			f.mu.Unlock()
			go func() {
				defer func() { _ = c.Close() }()
				var req daemonrpc.Request
				if json.NewDecoder(c).Decode(&req) != nil {
					return
				}
				f.mu.Lock()
				f.got = append(f.got, req.Method)
				f.mu.Unlock()
				data, _ := json.Marshal(rows)
				var resp daemonrpc.Response
				switch mode {
				case fakeAnswers:
					resp = daemonrpc.Response{Result: data}
				case fakeStarting:
					resp = daemonrpc.Response{Result: data, Starting: true}
				case fakeStartingErr:
					resp = daemonrpc.Response{Error: "daemon is starting", Starting: true}
				case fakeDaemonError:
					resp = daemonrpc.Response{Error: "refused: " + "x\x1b[2J"}
				case fakeBadReply:
					resp = daemonrpc.Response{Result: json.RawMessage(`"not a list"`)}
				case fakeHung:
					<-stop
					return
				}
				_ = json.NewEncoder(c).Encode(resp)
			}()
		}
	}()
	return f
}

// clusterConfig writes the client config the commands under test read.
func clusterConfig(t *testing.T, entries ...config.Cluster) {
	t.Helper()
	resolveFixture(t, false)
	t.Setenv("MARVEL_SESSION", "")
	if err := config.Save(&config.Config{Clusters: entries}); err != nil {
		t.Fatal(err)
	}
}

func fastTimer(t *testing.T, d time.Duration) {
	t.Helper()
	old := fanOutTimeout
	fanOutTimeout = d
	t.Cleanup(func() { fanOutTimeout = old })
}

// runFanGet runs the real command tree and returns the exit code, stdout and
// stderr.
func runFanGet(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	// The flags bind package variables that outlive the command; put them
	// back so a later test starts clean.
	t.Cleanup(func() { clusterName, socketPath, identityPath, clusterFlagGiven = "", "", "", false })
	root := newRootCmd()
	var errOut bytes.Buffer
	root.SetArgs(args)
	var code int
	out := captureStdout(t, func() { code = execute(root, &errOut) })
	return code, out, errOut.String()
}

func row(ws, name string) api.Session {
	return api.Session{Workspace: ws, Name: name, Team: "t", Role: "r", State: api.SessionRunning}
}

// The outcome set is closed; adding one is a decision, not an accident.
func TestOutcomeEnumIsClosed(t *testing.T) {
	want := []string{
		"answered", "starting", "same_daemon", "unreachable", "no_answer_in_time",
		"auth_refused", "host_key_unknown", "host_key_changed", "daemon_error", "bad_reply",
	}
	var got []string
	for _, o := range allOutcomes {
		got = append(got, string(o))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("outcomes = %v, want %v", got, want)
	}
	for _, o := range allOutcomes {
		if _, ok := outcomeExit[o]; !ok {
			t.Errorf("outcome %s has no exit code", o)
		}
	}
}

// Three clusters (answers, absent, hung) give three records and three status
// lines, and the set exits 10.
func TestFanOutOneOutcomePerCluster(t *testing.T) {
	fastTimer(t, 400*time.Millisecond)
	a := startFake(t, fakeAnswers, []api.Session{row("w", "s1")})
	h := startFake(t, fakeHung, nil)
	clusterConfig(t,
		config.Cluster{Name: "a", Socket: a.sock},
		config.Cluster{Name: "b", Socket: filepath.Join(t.TempDir(), "absent.sock")},
		config.Cluster{Name: "c", Socket: h.sock},
	)
	records, _, err := collectFanOut([]string{"a", "b", "c"}, false)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range records {
		got = append(got, r.Cluster+"="+string(r.Outcome))
	}
	if want := []string{"a=answered", "b=unreachable", "c=no_answer_in_time"}; !reflect.DeepEqual(got, want) {
		t.Errorf("records = %v, want %v", got, want)
	}
	code, _, stderr := runFanGet(t, "get", "sessions", "--cluster", "a,b,c")
	if code != 10 {
		t.Errorf("exit = %d, want 10", code)
	}
	if n := strings.Count(stderr, "marvel: cluster "); n != 3 {
		t.Errorf("%d status lines, want 3:\n%s", n, stderr)
	}
}

// No cluster answered and the causes differ: 11.
func TestFanOutNoneAnswered(t *testing.T) {
	fastTimer(t, 400*time.Millisecond)
	h := startFake(t, fakeHung, nil)
	clusterConfig(t,
		config.Cluster{Name: "b", Socket: filepath.Join(t.TempDir(), "absent.sock")},
		config.Cluster{Name: "c", Socket: h.sock},
	)
	code, _, _ := runFanGet(t, "get", "sessions", "--cluster", "b,c")
	if code != 11 {
		t.Errorf("exit = %d, want 11", code)
	}
}

// Every cluster failing for the same reason exits with that reason's code.
func TestAllFailSameCauseExitsThatCode(t *testing.T) {
	fastTimer(t, 400*time.Millisecond)
	t.Run("5 daemon_error", func(t *testing.T) {
		a := startFake(t, fakeDaemonError, nil)
		b := startFake(t, fakeDaemonError, nil)
		clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})
		if code, _, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b"); code != 5 {
			t.Errorf("exit = %d, want 5", code)
		}
	})
	t.Run("5 bad_reply", func(t *testing.T) {
		a := startFake(t, fakeBadReply, nil)
		b := startFake(t, fakeBadReply, nil)
		clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})
		if code, _, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b"); code != 5 {
			t.Errorf("exit = %d, want 5", code)
		}
	})
	t.Run("6 unreachable", func(t *testing.T) {
		d := t.TempDir()
		clusterConfig(t,
			config.Cluster{Name: "a", Socket: filepath.Join(d, "a.sock")},
			config.Cluster{Name: "b", Socket: filepath.Join(d, "b.sock")})
		if code, _, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b"); code != 6 {
			t.Errorf("exit = %d, want 6", code)
		}
	})
	t.Run("7 no_answer_in_time", func(t *testing.T) {
		a := startFake(t, fakeHung, nil)
		b := startFake(t, fakeHung, nil)
		clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})
		if code, _, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b"); code != 7 {
			t.Errorf("exit = %d, want 7", code)
		}
	})
	// 8 and 9 come from the SSH layer, which the unix fakes do not reach; the
	// mapping from the typed error is the unit under test, and the host key
	// rows below run it end to end.
	t.Run("8 auth_refused", func(t *testing.T) {
		for _, err := range []error{&daemonrpc.AuthRefusedError{}, &daemonrpc.NoIdentityError{}} {
			out, _, _, _ := classifyDialError(err, fanOutTimeout)
			if out != outcomeAuthRefused || outcomeExit[out] != 8 {
				t.Errorf("%T -> %s (exit %d), want auth_refused (8)", err, out, outcomeExit[out])
			}
		}
		if hint := nextStep(outcomeAuthRefused); !strings.Contains(hint, "the daemon refused the key offered") {
			t.Errorf("auth_refused hint = %q, want it to say the daemon refused the key offered", hint)
		}
	})
	t.Run("9 host_key", func(t *testing.T) {
		out, _, fp, _ := classifyDialError(&knownhosts.HostKeyUnknownError{Fingerprint: "SHA256:abc"}, fanOutTimeout)
		if out != outcomeHostKeyUnknown || outcomeExit[out] != 9 || fp != "SHA256:abc" {
			t.Errorf("unknown -> %s %q", out, fp)
		}
		out, _, fp, _ = classifyDialError(&knownhosts.HostKeyChangedError{Fingerprint: "SHA256:def"}, fanOutTimeout)
		if out != outcomeHostKeyChanged || outcomeExit[out] != 9 || fp != "SHA256:def" {
			t.Errorf("changed -> %s %q", out, fp)
		}
	})
}

// One hung cluster does not hold up the others past the timer.
func TestFanOutOneHungClusterBounded(t *testing.T) {
	fastTimer(t, 500*time.Millisecond)
	a := startFake(t, fakeAnswers, []api.Session{row("w", "s1")})
	h := startFake(t, fakeHung, nil)
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "h", Socket: h.sock})
	start := time.Now()
	code, stdout, _ := runFanGet(t, "get", "sessions", "--cluster", "a,h")
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %v with a 500ms timer", d)
	}
	if code != 10 {
		t.Errorf("exit = %d, want 10", code)
	}
	if !strings.Contains(stdout, "s1") {
		t.Errorf("the answering cluster's row is missing:\n%s", stdout)
	}
}

// Three hung clusters finish under twice the timer: the dials run in parallel.
func TestFanOutIsParallel(t *testing.T) {
	timer := 600 * time.Millisecond
	fastTimer(t, timer)
	var entries []config.Cluster
	for _, n := range []string{"a", "b", "c"} {
		entries = append(entries, config.Cluster{Name: n, Socket: startFake(t, fakeHung, nil).sock})
	}
	clusterConfig(t, entries...)
	start := time.Now()
	runFanGet(t, "get", "sessions", "--cluster", "a,b,c")
	if d := time.Since(start); d >= 2*timer {
		t.Errorf("three hung clusters took %v, want under %v", d, 2*timer)
	}
}

// A fan-out reads: the methods a cluster sees are within the allowlist.
func TestFanOutCallsOnlyReadMethods(t *testing.T) {
	a := startFake(t, fakeAnswers, nil)
	b := startFake(t, fakeAnswers, nil)
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})
	runFanGet(t, "get", "sessions", "--cluster", "a,b")
	allowed := map[string]bool{}
	for _, m := range fanOutMethods {
		allowed[m] = true
	}
	for _, f := range []*fakeCluster{a, b} {
		ms := f.methods()
		if len(ms) == 0 {
			t.Error("a cluster saw no call")
		}
		for _, m := range ms {
			if !allowed[m] {
				t.Errorf("method %q is not in the read allowlist %v", m, fanOutMethods)
			}
		}
	}
}

// MARVEL_SESSION set: the view is refused as a command error, and no cluster
// is dialled.
func TestFanOutRefusesIdentitySocketSeat(t *testing.T) {
	a := startFake(t, fakeAnswers, nil)
	b := startFake(t, fakeAnswers, nil)
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})
	t.Setenv("MARVEL_SESSION", "ws/seat-1")
	code, _, stderr := runFanGet(t, "get", "sessions", "--cluster", "a,b")
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "MARVEL_SESSION") {
		t.Errorf("stderr does not name MARVEL_SESSION:\n%s", stderr)
	}
	if a.conns()+b.conns() != 0 {
		t.Errorf("%d connections made, want 0", a.conns()+b.conns())
	}
}

// A starting daemon's rows are shown and may be incomplete: under a cluster
// set that is a partial answer, exit 10, never 11.
func TestStartingReplyIsPartial(t *testing.T) {
	for _, args := range [][]string{{"--cluster", "a,b"}, {"--all-clusters"}} {
		a := startFake(t, fakeStarting, []api.Session{row("w", "s1")})
		b := startFake(t, fakeAnswers, []api.Session{row("w", "s2")})
		clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})
		code, stdout, _ := runFanGet(t, append([]string{"get", "sessions"}, args...)...)
		if code != 10 {
			t.Errorf("%v: exit = %d, want 10", args, code)
		}
		if !strings.Contains(stdout, "s1") || !strings.Contains(stdout, "s2") {
			t.Errorf("%v: rows are missing:\n%s", args, stdout)
		}
	}
	// Both starting, one with a refusal and no rows: still 10.
	a := startFake(t, fakeStarting, []api.Session{row("w", "s1")})
	b := startFake(t, fakeStartingErr, nil)
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})
	if code, _, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b"); code != 10 {
		t.Errorf("both starting: exit = %d, want 10", code)
	}
}

// R4: a plain get sessions keeps today's exit when the daemon is starting.
func TestPlainGetSessionsStartingKeepsExit(t *testing.T) {
	a := startFake(t, fakeStarting, []api.Session{row("w", "s1")})
	clusterConfig(t)
	t.Setenv(config.SocketEnv, a.sock)
	code, _, _ := runFanGet(t, "get", "sessions")
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
}

// R4: --cluster naming one cluster is not a cluster set; it keeps today's exit
// and today's table, with no KEY column.
func TestSingleClusterFlagStartingKeepsExit(t *testing.T) {
	a := startFake(t, fakeStarting, []api.Session{row("w", "s1")})
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock})
	code, stdout, _ := runFanGet(t, "get", "sessions", "--cluster", "a")
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if strings.Contains(stdout, "KEY") {
		t.Errorf("a single cluster drew a KEY column:\n%s", stdout)
	}
}

// Rows sort cluster first, then workspace and name.
func TestFanOutSortsClusterFirst(t *testing.T) {
	a := startFake(t, fakeAnswers, []api.Session{row("z", "zz"), row("a", "aa")})
	b := startFake(t, fakeAnswers, []api.Session{row("a", "aa")})
	clusterConfig(t, config.Cluster{Name: "b", Socket: b.sock}, config.Cluster{Name: "a", Socket: a.sock})
	_, stdout, _ := runFanGet(t, "get", "sessions", "--cluster", "b,a")
	var keys []string
	for _, l := range strings.Split(stdout, "\n") {
		if f := strings.Fields(l); len(f) > 0 && strings.Contains(f[0], ":") {
			keys = append(keys, f[0])
		}
	}
	if want := []string{"a:a/aa", "a:z/zz", "b:a/aa"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
}

// The KEY column is the first column and survives a narrow terminal.
func TestKeyColumnSurvivesNarrowWidth(t *testing.T) {
	a := startFake(t, fakeAnswers, []api.Session{row("w", "s1")})
	b := startFake(t, fakeAnswers, []api.Session{row("w", "s2")})
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})
	old := terminalWidth
	terminalWidth = func() int { return 40 }
	t.Cleanup(func() { terminalWidth = old })
	_, stdout, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b")
	lines := strings.Split(stdout, "\n")
	if !strings.HasPrefix(lines[0], "KEY") {
		t.Errorf("the first column is not KEY:\n%s", stdout)
	}
	if !strings.Contains(stdout, "a:w/s1") || !strings.Contains(stdout, "b:w/s2") {
		t.Errorf("keys are missing:\n%s", stdout)
	}
}

// Explicit --columns keep KEY in front.
func TestKeyColumnWithExplicitColumns(t *testing.T) {
	a := startFake(t, fakeAnswers, []api.Session{row("w", "s1")})
	b := startFake(t, fakeAnswers, []api.Session{row("w", "s2")})
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})
	_, stdout, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b", "--columns", "state")
	lines := strings.Split(stdout, "\n")
	f := strings.Fields(lines[0])
	if len(f) < 2 || f[0] != "KEY" || f[1] != "STATE" {
		t.Errorf("header = %q, want KEY then STATE", lines[0])
	}
}

// Piped (no terminal), the KEY column is there too.
func TestKeyColumnInPipe(t *testing.T) {
	a := startFake(t, fakeAnswers, []api.Session{row("w", "s1")})
	b := startFake(t, fakeAnswers, []api.Session{row("w", "s2")})
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})
	old := terminalWidth
	terminalWidth = func() int { return 0 }
	t.Cleanup(func() { terminalWidth = old })
	_, stdout, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b")
	if !strings.HasPrefix(stdout, "KEY") {
		t.Errorf("piped output does not start with KEY:\n%s", stdout)
	}
}

// Flag rules, all refused as command errors (exit 1) before any dial.
func TestClusterSetFlagRules(t *testing.T) {
	a := startFake(t, fakeAnswers, nil)
	b := startFake(t, fakeAnswers, nil)
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})
	cases := []struct {
		name string
		args []string
	}{
		{"comma list on a write", []string{"scale", "w/t", "--role", "r", "--replicas", "1", "--cluster", "a,b"}},
		{"comma list on another read", []string{"get", "teams", "--cluster", "a,b"}},
		{"identity with a set", []string{"get", "sessions", "--cluster", "a,b", "--identity", "/nonexistent"}},
		{"socket with a set", []string{"get", "sessions", "--cluster", "a,b", "--socket", "/nonexistent"}},
		{"socket with all", []string{"get", "sessions", "--all-clusters", "--socket", "/nonexistent"}},
		{"cluster with all", []string{"get", "sessions", "--cluster", "a,b", "--all-clusters"}},
		{"unknown cluster in a set", []string{"get", "sessions", "--cluster", "a,nope"}},
		{"duplicate in a set", []string{"get", "sessions", "--cluster", "a,a"}},
		{"empty member", []string{"get", "sessions", "--cluster", "a,"}},
		{"all-clusters on teams", []string{"get", "teams", "--all-clusters"}},
	}
	for _, c := range cases {
		code, _, stderr := runFanGet(t, c.args...)
		if code != 1 {
			t.Errorf("%s: exit = %d, want 1\n%s", c.name, code, stderr)
		}
	}
	if a.conns()+b.conns() != 0 {
		t.Errorf("%d connections made by refused commands, want 0", a.conns()+b.conns())
	}
}

// --- the SSH-facing rows -------------------------------------------------

// hostKeyServer listens on 127.0.0.1:0 and presents a host key, which is all
// a client that refuses the key needs. It returns the host:port.
func hostKeyServer(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				if sc, _, _, err := ssh.NewServerConn(c, cfg); err == nil {
					_ = sc.Close()
				}
			}()
		}
	}()
	return ln.Addr().String()
}

// identityFile writes a fresh private key and returns its path.
func identityFile(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "id")
	if err := os.WriteFile(p, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// An unknown host key is refused without a prompt: stdin is never read and
// known_hosts is untouched.
func TestFanOutNeverReadsStdin(t *testing.T) {
	addr := hostKeyServer(t)
	good := startFake(t, fakeAnswers, []api.Session{row("w", "s1")})
	clusterConfig(t,
		config.Cluster{Name: "a", Socket: good.sock},
		config.Cluster{Name: "u", Server: "mrvl://rig@" + addr, Identity: identityFile(t)})
	layout, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.EnsureHome(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.KnownHosts(), []byte("# recorded hosts\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, beforeErr := os.ReadFile(layout.KnownHosts())

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("y"); err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = oldStdin; _ = r.Close() })

	code, _, stderr := runFanGet(t, "get", "sessions", "--cluster", "a,u")
	_ = w.Close()
	left, _ := io.ReadAll(r)
	if string(left) != "y" {
		t.Errorf("stdin held %q after the run, want the unread %q", left, "y")
	}
	after, afterErr := os.ReadFile(layout.KnownHosts())
	if !bytes.Equal(before, after) || (beforeErr == nil) != (afterErr == nil) {
		t.Errorf("known_hosts changed during the fan-out")
	}
	if code != 10 || !strings.Contains(stderr, "host_key_unknown") {
		t.Errorf("exit = %d, stderr:\n%s", code, stderr)
	}
}

// A changed host key reads differently from an unknown one, every time, even
// when the set exits 11.
func TestFanOutChangedKeyDistinct(t *testing.T) {
	addr := hostKeyServer(t)
	clusterConfig(t, config.Cluster{Name: "u", Server: "mrvl://rig@" + addr, Identity: identityFile(t)},
		config.Cluster{Name: "b", Socket: filepath.Join(t.TempDir(), "absent.sock")})
	layout, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.EnsureHome(); err != nil {
		t.Fatal(err)
	}
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	other, err := ssh.NewSignerFromKey(otherPriv)
	if err != nil {
		t.Fatal(err)
	}
	if err := knownhosts.Trust(layout, addr, other.PublicKey()); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runFanGet(t, "get", "sessions", "--cluster", "u,b")
	if code != 11 {
		t.Errorf("exit = %d, want 11 (causes differ)", code)
	}
	if !strings.Contains(stderr, "cluster u: host_key_changed") {
		t.Errorf("the changed key is not named:\n%s", stderr)
	}
	if strings.Contains(stderr, "host_key_unknown") {
		t.Errorf("a changed key was reported as unknown:\n%s", stderr)
	}
}

// A cause that holds the daemon's text is filtered on the status line.
func TestStatusLineFiltersDaemonText(t *testing.T) {
	a := startFake(t, fakeDaemonError, nil)
	b := startFake(t, fakeAnswers, nil)
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})
	_, _, stderr := runFanGet(t, "get", "sessions", "--cluster", "a,b")
	if strings.Contains(stderr, "\x1b") {
		t.Errorf("a status line holds ESC: %q", stderr)
	}
	if !strings.Contains(stderr, "cluster a: daemon_error") {
		t.Errorf("no daemon_error line for a:\n%s", stderr)
	}
}

var (
	_ = fmt.Sprintf
	_ = errors.New
)

// Until #829 lands, any key holding ':' reads as qualified (plan section 2,
// line 55, ruled 2026-10-10), so a key the operator TYPES that holds ':' is
// refused at exit 1 before any dial, with one line naming #829 (plan section 3,
// line 97). A row the daemon RETURNS whose workspace or name holds ':' is a
// different thing: it is drawn as usual, filtered, and the set still exits 0.
func TestColonKeyRefusedUntil829(t *testing.T) {
	a := startFake(t, fakeAnswers, []api.Session{row("w", "ok"), row("w", "bad:name")})
	b := startFake(t, fakeAnswers, []api.Session{row("w:x", "fine")})
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})

	for _, args := range [][]string{
		{"kill", "w:seat-1", "--cluster", "a"},
		{"inject", "a:w/seat-1", "hello", "--cluster", "a"},
		{"capture", "w:seat-1", "--cluster", "a"},
		{"describe", "session", "w:seat-1", "--cluster", "a"},
		{"view", "refresh", "w:seat-1", "--cluster", "a"},
	} {
		code, _, stderr := runFanGet(t, args...)
		if code != 1 {
			t.Errorf("%v: exit = %d, want 1", args, code)
		}
		if n := strings.Count(stderr, "#829"); n != 1 {
			t.Errorf("%v: %d lines name #829, want 1:\n%s", args, n, stderr)
		}
	}
	if a.conns()+b.conns() != 0 {
		t.Errorf("%d connections made before the refusal, want 0", a.conns()+b.conns())
	}

	// A daemon row holding ':' is drawn, and the exit stays 0.
	code, stdout, stderr := runFanGet(t, "get", "sessions", "--cluster", "a,b")
	if code != 0 {
		t.Errorf("exit = %d, want 0 for a set whose rows hold ':':\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "bad:name") || !strings.Contains(stdout, "w:x") {
		t.Errorf("a daemon row holding ':' was left out:\n%s", stdout)
	}
}

// keyWords are the words that mark a Use-line placeholder as a session or
// team key: <session-key>, <workspace/team>, [workspace/session], <key>.
var keyWords = map[string]bool{"key": true, "session": true, "team": true, "workspace": true, "seat": true}

// notKeyCommands are commands whose placeholder carries a key word but names
// something else. Each entry says what.
var notKeyCommands = map[string]string{
	"marvel keys authorize": "<public-key-file> is a file of an SSH public key, not a session key",
}

var placeholderRe = regexp.MustCompile(`[<\[]([^>\]]*)[>\]]`)

// takesKey reports whether a command's Use line has a placeholder made of
// key words.
func takesKey(c *cobra.Command) bool {
	if _, skip := notKeyCommands[c.CommandPath()]; skip {
		return false
	}
	for _, m := range placeholderRe.FindAllStringSubmatch(c.Use, -1) {
		for _, w := range strings.FieldsFunc(m[1], func(r rune) bool { return r == '/' || r == '-' || r == ' ' || r == '|' }) {
			if keyWords[w] {
				return true
			}
		}
	}
	return false
}

// uncoveredKeyCommands walks a command tree and returns the commands that
// take a key and are not covered by the refusal of a key holding ':', and how
// many key-taking commands it found.
func uncoveredKeyCommands(root *cobra.Command) (uncovered []string, found int) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if takesKey(c) {
			found++
			if _, ok := typedKey(c, []string{"w:key"}); !ok {
				uncovered = append(uncovered, c.CommandPath())
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	return uncovered, found
}

// Every command that takes a session or team key must be covered by the
// refusal of a key holding ':', so a command added later cannot slip past it.
func TestEveryKeyTakingCommandRefusesAColonKey(t *testing.T) {
	uncovered, found := uncoveredKeyCommands(newRootCmd())
	for _, c := range uncovered {
		t.Errorf("%q takes a key but typedKey does not cover it", c)
	}
	if found < 8 {
		t.Errorf("the walker found only %d key-taking commands; the matcher may have drifted", found)
	}
}

// The walker must see a new command that names its key in a way nobody
// listed: a `poke <key>` command added to the tree is reported.
func TestKeyWalkerSeesANewCommand(t *testing.T) {
	root := newRootCmd()
	root.AddCommand(&cobra.Command{Use: "poke <key>"})
	uncovered, _ := uncoveredKeyCommands(root)
	if !reflect.DeepEqual(uncovered, []string{"marvel poke"}) {
		t.Errorf("uncovered = %v, want [marvel poke]", uncovered)
	}
}

// The table and the tree do not drift the other way either: every command
// the table names exists.
func TestKeyTableNamesOnlyRealCommands(t *testing.T) {
	root := newRootCmd()
	real := map[string]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		real[c.CommandPath()] = true
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	for path := range keyArgCommands {
		if !real[path] {
			t.Errorf("keyArgCommands names %q, which is not a command", path)
		}
	}
}

// A cluster with no loadable key is refused before any dial, and its status
// line says that, not that a daemon refused a key.
func TestNoIdentityHintSaysNoDialWasMade(t *testing.T) {
	good := startFake(t, fakeAnswers, nil)
	var mu sync.Mutex
	var peers []string
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			peers = append(peers, c.RemoteAddr().String())
			mu.Unlock()
			_ = c.Close()
		}
	}()
	clusterConfig(t,
		config.Cluster{Name: "a", Socket: good.sock},
		config.Cluster{Name: "n", Server: "mrvl://rig@" + ln.Addr().String()})
	_, _, stderr := runFanGet(t, "get", "sessions", "--cluster", "a,n")
	var line string
	for _, l := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(l, "marvel: cluster n: auth_refused") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no auth_refused line for n:\n%s", stderr)
	}
	if strings.Contains(line, "refused the key") {
		t.Errorf("the line says a daemon refused keys, but no dial was made: %s", line)
	}
	if !strings.Contains(line, "no dial was made") {
		t.Errorf("the line does not say no dial was made: %s", line)
	}
	// The peers list is filled by the accept goroutine, which can trail the
	// run. A probe dial is accepted after every earlier dial, so once it shows
	// in the list every connection the run made does too.
	probe, err := net.DialTimeout("tcp", ln.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatalf("probe dial: %v", err)
	}
	defer func() { _ = probe.Close() }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n, seen := len(peers), false
		for _, p := range peers {
			seen = seen || p == probe.LocalAddr().String()
		}
		mu.Unlock()
		if seen {
			if n-1 != 0 {
				t.Errorf("the listener saw %d connections before the probe, want 0", n-1)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the accept loop never saw the probe connection")
		}
		time.Sleep(time.Millisecond)
	}
}
