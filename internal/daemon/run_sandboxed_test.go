package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// run.sandboxed starts a command on a seat's behalf, so like handoff.put it is
// served only on the daemon's own socket, never to a credential-push key.
func TestRunSandboxedIsLocalOnly(t *testing.T) {
	if !methodRequiresLocal(daemonrpc.MethodRunSandboxed) {
		t.Fatal("run.sandboxed is not in localOnlyMethods")
	}
	if methodAllowedForScope(daemonrpc.MethodRunSandboxed, ScopeCredentialPush) {
		t.Fatal("a credential-push key may call run.sandboxed")
	}
}

func runDaemon(t *testing.T) (*Daemon, *api.Store, api.Session) {
	t.Helper()
	store := api.NewStore()
	d := &Daemon{store: store}
	if err := store.CreateWorkspace(&api.Workspace{Name: "ws", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	tok, hash, err := api.NewHeartbeatToken()
	if err != nil {
		t.Fatal(err)
	}
	sess := &api.Session{
		Name: "squad-builder-g1-0", Workspace: "ws", Team: "squad", Role: "builder", Generation: 1,
		Runtime: api.Runtime{Name: "claude", Command: "claude"}, State: api.SessionRunning,
		CreatedAt: time.Now().UTC(), HeartbeatToken: tok, HeartbeatTokenHash: hash,
	}
	if err := store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	return d, store, *sess
}

// The token is the identity: a bound token of a running seat resolves to that
// seat, and the request names no session.
func TestAuthorizeRunResolvesTheSeatFromTheToken(t *testing.T) {
	d, _, sess := runDaemon(t)
	got, err := d.authorizeRun(sess.HeartbeatToken)
	if err != nil {
		t.Fatalf("authorizeRun refused a bound token of a running seat: %v", err)
	}
	if got.Key() != sess.Key() {
		t.Fatalf("resolved %q, want %q", got.Key(), sess.Key())
	}
}

func TestAuthorizeRunRefusesWhatIsNotABoundRunningSeat(t *testing.T) {
	d, store, sess := runDaemon(t)
	unbound := &api.Session{
		Name: "squad-old-g1-0", Workspace: "ws", Team: "squad", Role: "old", Generation: 1,
		Runtime: api.Runtime{Name: "claude", Command: "claude"}, State: api.SessionRunning,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateSession(unbound); err != nil {
		t.Fatal(err)
	}
	for name, token := range map[string]string{
		"empty token":   "",
		"unknown token": "not-a-token",
	} {
		if _, err := d.authorizeRun(token); err == nil {
			t.Errorf("%s: authorizeRun admitted it", name)
		}
	}
	// A record with no token hash is never found by token, so an unbound seat
	// cannot ask for a run (the heartbeat fail-open does not carry over).
	if _, err := d.authorizeRun(""); err == nil {
		t.Error("an empty token matched an unbound record")
	}
	if err := store.UpdateSession(sess.Key(), func(s *api.Session) error {
		s.State = api.SessionSucceeded
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.authorizeRun(sess.HeartbeatToken); err == nil {
		t.Error("a seat that is no longer running was admitted")
	}
}

// The log line names the seat, the program, the profile by digest and the
// outcome, and nothing a seat should not see echoed.
func TestRunLogRecordCarriesNoValues(t *testing.T) {
	_, _, sess := runDaemon(t)
	req := api.RunSandboxedRequest{
		Argv:    []string{"/usr/bin/true", "ARG-SECRET-1"},
		Env:     []string{"TOKEN=ENV-SECRET-2"},
		Stdin:   []byte("STDIN-SECRET-3"),
		Profile: "(deny network*) ;PROFILE-TEXT-4",
	}
	composed := "(version 1)\n" + req.Profile
	sum := sha256.Sum256([]byte(composed))
	line := runLogRecord(sess, req, composed, 7, 1500*time.Millisecond)
	for _, want := range []string{sess.Key(), "/usr/bin/true", hex.EncodeToString(sum[:]), "7"} {
		if !strings.Contains(line, want) {
			t.Errorf("log line lacks %q: %q", want, line)
		}
	}
	for _, leak := range []string{"ARG-SECRET-1", "ENV-SECRET-2", "STDIN-SECRET-3", "PROFILE-TEXT-4", sess.HeartbeatToken} {
		if strings.Contains(line, leak) {
			t.Errorf("log line carries %q: %q", leak, line)
		}
	}
}

// Fidelity: output and exit status come back as a direct run gives them.
func TestRunChildReturnsOutputAndExitStatus(t *testing.T) {
	res, err := runChild(context.Background(),
		[]string{"/bin/sh", "-c", "cat; echo out; echo err >&2; exit 3"},
		t.TempDir(), []string{"PATH=/usr/bin:/bin"}, []byte("in\n"), 10*time.Second, api.MaxRunOutputBytes)
	if err != nil {
		t.Fatalf("runChild: %v", err)
	}
	if res.ExitCode != 3 || res.Stdout != "in\nout\n" || res.Stderr != "err\n" {
		t.Fatalf("got rc=%d stdout=%q stderr=%q, want rc=3 stdout=%q stderr=%q", res.ExitCode, res.Stdout, res.Stderr, "in\nout\n", "err\n")
	}
}

// Fidelity: the working directory and the environment are the request's, and
// only the request's.
func TestRunChildUsesTheRequestedDirAndEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MARVEL_RUN_LEAK_PROBE", "daemon-value")
	res, err := runChild(context.Background(),
		[]string{"/bin/sh", "-c", `pwd -P; echo "$ASKED"; echo "${MARVEL_RUN_LEAK_PROBE-unset}"`},
		dir, []string{"PATH=/usr/bin:/bin", "ASKED=yes"}, nil, 10*time.Second, api.MaxRunOutputBytes)
	if err != nil {
		t.Fatalf("runChild: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	if len(lines) != 3 || !strings.HasSuffix(lines[0], dirBase(dir)) || lines[1] != "yes" || lines[2] != "unset" {
		t.Fatalf("stdout = %q, want the requested dir, ASKED=yes, and the daemon's variable unset", res.Stdout)
	}
}

func dirBase(p string) string { return p[strings.LastIndex(p, "/")+1:] }

// Lifetime: the daemon owns the child. At the timeout it kills the whole
// process group, grandchildren included, and says so.
func TestRunChildKillsTheGroupAtTheTimeout(t *testing.T) {
	start := time.Now()
	res, err := runChild(context.Background(),
		[]string{"/bin/sh", "-c", "sleep 60 & echo $!; wait"},
		t.TempDir(), []string{"PATH=/usr/bin:/bin"}, nil, 300*time.Millisecond, api.MaxRunOutputBytes)
	if err != nil {
		t.Fatalf("runChild: %v", err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("runChild took %s at a 300ms timeout", took)
	}
	if !res.TimedOut {
		t.Error("TimedOut is false for a run killed at the limit")
	}
	assertGone(t, res.Stdout)
}

// Lifetime: when the request ends (the seat died or hung up), ctx ends and the
// group is killed.
func TestRunChildKillsTheGroupWhenTheRequestEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(300 * time.Millisecond); cancel() }()
	res, err := runChild(ctx, []string{"/bin/sh", "-c", "sleep 60 & echo $!; wait"},
		t.TempDir(), []string{"PATH=/usr/bin:/bin"}, nil, time.Minute, api.MaxRunOutputBytes)
	if err != nil {
		t.Fatalf("runChild: %v", err)
	}
	assertGone(t, res.Stdout)
}

func assertGone(t *testing.T, stdout string) {
	t.Helper()
	pid, err := strconv.Atoi(strings.TrimSpace(stdout))
	if err != nil || pid <= 1 {
		t.Fatalf("the child printed no grandchild pid: %q", stdout)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("grandchild %d is still alive after the run ended", pid)
}

// Output is capped per stream and the cap is reported, never silent.
func TestRunChildCapsOutput(t *testing.T) {
	res, err := runChild(context.Background(),
		[]string{"/bin/sh", "-c", "head -c 100000 /dev/zero | tr '\\0' x"},
		t.TempDir(), []string{"PATH=/usr/bin:/bin"}, nil, 10*time.Second, 1024)
	if err != nil {
		t.Fatalf("runChild: %v", err)
	}
	if len(res.Stdout) != 1024 || !res.Truncated {
		t.Fatalf("stdout is %d bytes, truncated=%v, want 1024 and true", len(res.Stdout), res.Truncated)
	}
}
