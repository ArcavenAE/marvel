package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/session"
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
	// cannot ask for a run (the heartbeat fail-open does not carry over). With
	// only the unbound record's key known, no token reaches it.
	for _, tok := range []string{unbound.Key(), "x", strings.Repeat("0", 64)} {
		got, err := d.authorizeRun(tok)
		if err == nil && got.HeartbeatTokenHash == "" {
			t.Errorf("token %q admitted the unbound record %s", tok, got.Key())
		}
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
		Profile: "(deny network*) ;PROFILE-TEXT-4",
	}
	composed := "(version 1)\n" + req.Profile
	sum := sha256.Sum256([]byte(composed))
	line := runLogRecord(sess, req, composed, 7, 1500*time.Millisecond)
	for _, want := range []string{"seat=" + sess.Key(), "argv0=/usr/bin/true", "profile_sha256=" + hex.EncodeToString(sum[:]), "rc=7 ", "took=1.5s"} {
		if !strings.Contains(line, want) {
			t.Errorf("log line lacks %q: %q", want, line)
		}
	}
	for _, leak := range []string{"ARG-SECRET-1", "ENV-SECRET-2", "PROFILE-TEXT-4", sess.HeartbeatToken} {
		if strings.Contains(line, leak) {
			t.Errorf("log line carries %q: %q", leak, line)
		}
	}
}

// Fidelity: output and exit status come back as a direct run gives them.
func TestRunChildReturnsOutputAndExitStatus(t *testing.T) {
	res, err := runChild(context.Background(),
		[]string{"/bin/sh", "-c", "echo out; echo err >&2; exit 3"},
		t.TempDir(), []string{"PATH=/usr/bin:/bin"}, 10*time.Second, api.MaxRunOutputBytes)
	if err != nil {
		t.Fatalf("runChild: %v", err)
	}
	if res.ExitCode != 3 || res.Stdout != "out\n" || res.Stderr != "err\n" {
		t.Fatalf("got rc=%d stdout=%q stderr=%q, want rc=3 stdout=%q stderr=%q", res.ExitCode, res.Stdout, res.Stderr, "out\n", "err\n")
	}
}

// Fidelity: the working directory and the environment are the request's, and
// only the request's.
func TestRunChildUsesTheRequestedDirAndEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MARVEL_RUN_LEAK_PROBE", "daemon-value")
	res, err := runChild(context.Background(),
		[]string{"/bin/sh", "-c", `pwd -P; echo "$ASKED"; echo "${MARVEL_RUN_LEAK_PROBE-unset}"`},
		dir, []string{"PATH=/usr/bin:/bin", "ASKED=yes"}, 10*time.Second, api.MaxRunOutputBytes)
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
		t.TempDir(), []string{"PATH=/usr/bin:/bin"}, 300*time.Millisecond, api.MaxRunOutputBytes)
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
		t.TempDir(), []string{"PATH=/usr/bin:/bin"}, time.Minute, api.MaxRunOutputBytes)
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
		t.TempDir(), []string{"PATH=/usr/bin:/bin"}, 10*time.Second, 1024)
	if err != nil {
		t.Fatalf("runChild: %v", err)
	}
	if len(res.Stdout) != 1024 || !res.Truncated {
		t.Fatalf("stdout is %d bytes, truncated=%v, want 1024 and true", len(res.Stdout), res.Truncated)
	}
}

// R10: the stderr cap, and a command that cannot start is an error and not a
// child's exit status.
func TestRunChildCapsStderrToo(t *testing.T) {
	res, err := runChild(context.Background(),
		[]string{"/bin/sh", "-c", "head -c 100000 /dev/zero | tr '\\0' x >&2"},
		t.TempDir(), []string{"PATH=/usr/bin:/bin"}, 10*time.Second, 1024)
	if err != nil {
		t.Fatalf("runChild: %v", err)
	}
	if len(res.Stderr) != 1024 || !res.Truncated {
		t.Fatalf("stderr is %d bytes, truncated=%v, want 1024 and true", len(res.Stderr), res.Truncated)
	}
}

func TestRunChildReportsAnExecFailureAsAnError(t *testing.T) {
	res, err := runChild(context.Background(), []string{"/no/such/binary"}, t.TempDir(), nil, 10*time.Second, 1024)
	if err == nil {
		t.Fatalf("a missing program gave no error, result %+v", res)
	}
}

// R7: the carve-out is the seat's recorded workdir, never the request's Dir.
func TestRunCarveOutIsTheRecordedWorkDir(t *testing.T) {
	_, _, sess := runDaemon(t)
	sess.WorkDir = "/work/tree"
	got := runCarveOut(sess, api.RunSandboxedRequest{Dir: "/Users/x/Documents"})
	if len(got) != 1 || got[0] != "/work/tree" {
		t.Fatalf("carve-out = %q, want only the recorded workdir", got)
	}
}

// R8, handler level: a request the daemon cannot authorize is answered with an
// error and runs nothing. The marker file is created only if the command ran.
func TestHandleRunSandboxedRefusesAnUnknownTokenAndRunsNothing(t *testing.T) {
	d, _, _ := runDaemon(t)
	marker := filepath.Join(t.TempDir(), "ran")
	body, err := json.Marshal(api.RunSandboxedRequest{Token: "not-a-token", Argv: []string{"/usr/bin/touch", marker}})
	if err != nil {
		t.Fatal(err)
	}
	resp := d.handleRunSandboxed(context.Background(), body)
	if resp.Error == "" {
		t.Fatalf("an unknown token was answered without an error: %+v", resp)
	}
	if _, serr := os.Stat(marker); serr == nil {
		t.Fatal("the command ran for an unauthorized request")
	}
	if strings.Contains(resp.Error, "not-a-token") {
		t.Errorf("the refusal echoes the token: %q", resp.Error)
	}
}

// handlerFixture is a daemon whose seat holds a role, with a fake sandbox-exec
// that records every invocation, so a refusal can be told from a run.
func handlerFixture(t *testing.T) (d *Daemon, sess api.Session, invoked string) {
	t.Helper()
	d, store, sess := runDaemon(t)
	if err := store.CreateTeam(&api.Team{
		Name: "squad", Workspace: "ws", Generation: 1, CreatedAt: time.Now().UTC(),
		Roles: []api.Role{{Name: "builder", Replicas: 1, Runtime: api.Runtime{Name: "claude", Command: "claude"}}},
	}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	invoked = filepath.Join(dir, "invoked")
	fakeExec := filepath.Join(dir, "sandbox-exec")
	if err := os.WriteFile(fakeExec, []byte("#!/bin/sh\necho x >> "+invoked+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	d.runGOOS, d.runSandboxExec = "darwin", fakeExec
	return d, sess, invoked
}

// G1 (i): with a valid token, a request whose profile is outside the grammar is
// refused before anything is started.
func TestHandleRunSandboxedRefusesARefusedProfileWithAValidToken(t *testing.T) {
	d, sess, invoked := handlerFixture(t)
	body, err := json.Marshal(api.RunSandboxedRequest{
		Token: sess.HeartbeatToken, Argv: []string{"/usr/bin/true"}, Profile: "(allow default)\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := d.handleRunSandboxed(context.Background(), body)
	if !strings.Contains(resp.Error, "run profile refused") {
		t.Fatalf("error = %q, want it to name the refused run profile", resp.Error)
	}
	if _, serr := os.Stat(invoked); serr == nil {
		t.Fatal("sandbox-exec was started for a refused profile")
	}
}

// G1 (ii): a host that cannot sandbox refuses the run; nothing runs unwrapped.
func TestHandleRunSandboxedRefusesAHostThatCannotSandbox(t *testing.T) {
	d, sess, invoked := handlerFixture(t)
	marker := filepath.Join(t.TempDir(), "ran")
	body, err := json.Marshal(api.RunSandboxedRequest{
		Token: sess.HeartbeatToken, Argv: []string{"/usr/bin/touch", marker}, Profile: "(deny network*)\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	d.runGOOS = "linux"
	resp := d.handleRunSandboxed(context.Background(), body)
	if !strings.Contains(resp.Error, "no sandbox on this host") {
		t.Fatalf("error = %q, want it to name the sandbox host check", resp.Error)
	}
	d.runGOOS, d.runSandboxExec = "darwin", filepath.Join(t.TempDir(), "missing")
	resp = d.handleRunSandboxed(context.Background(), body)
	if !strings.Contains(resp.Error, "no sandbox on this host") {
		t.Fatalf("a missing sandbox-exec: error = %q, want it to name the sandbox host check", resp.Error)
	}
	for _, p := range []string{marker, invoked} {
		if _, serr := os.Stat(p); serr == nil {
			t.Fatalf("something ran on a host that cannot sandbox: %s exists", p)
		}
	}
}

// greenFixture is handlerFixture with a real session manager over a scratch
// profile directory, and a runner seam that records what it was asked to exec.
func greenFixture(t *testing.T) (d *Daemon, sess api.Session, got *[]string) {
	t.Helper()
	d, sess, _ = handlerFixture(t)
	m := session.NewManager(d.store, nil)
	m.GOOS, m.SandboxExec = "darwin", d.runSandboxExec
	m.SandboxDir, m.RealHome = t.TempDir(), t.TempDir()
	m.NetMounts = func() []string { return nil }
	m.TCCDeny = true
	d.sessMgr = m
	var argv []string
	d.runExec = func(ctx context.Context, a []string, dir string, env []string, timeout time.Duration, maxOut int) (api.RunSandboxedResult, error) {
		argv = a
		return api.RunSandboxedResult{ExitCode: 5, Stdout: "ok"}, nil
	}
	return d, sess, &argv
}

// An authorized run execs sandbox-exec -f <staged> argv, and the staged bytes
// are the composed profile with the deny block last.
func TestHandleRunSandboxedExecsTheComposedProfile(t *testing.T) {
	d, sess, got := greenFixture(t)
	body, err := json.Marshal(api.RunSandboxedRequest{
		Token: sess.HeartbeatToken, Argv: []string{"/usr/bin/true", "a"}, Profile: "(deny network*)\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := d.handleRunSandboxed(context.Background(), body)
	if resp.Error != "" {
		t.Fatalf("an authorized run was refused: %s", resp.Error)
	}
	var res api.RunSandboxedResult
	if err := json.Unmarshal(resp.Result, &res); err != nil || res.ExitCode != 5 || res.Stdout != "ok" {
		t.Fatalf("result = %+v (%v), want the runner's rc 5 and stdout", res, err)
	}
	a := *got
	if len(a) < 5 || a[0] != d.runSandboxExec || a[1] != "-f" || a[len(a)-2] != "/usr/bin/true" || a[len(a)-1] != "a" {
		t.Fatalf("argv = %q, want sandbox-exec -f <staged> ... /usr/bin/true a", a)
	}
	if _, serr := os.Stat(a[2]); serr == nil {
		t.Errorf("the staged profile %s was left behind", a[2])
	}
}

// ctx ending (the seat hung up) reaches the runner.
func TestHandleRunSandboxedPassesCtxEndToTheRunner(t *testing.T) {
	d, sess, _ := greenFixture(t)
	seen := make(chan struct{})
	d.runExec = func(ctx context.Context, a []string, dir string, env []string, timeout time.Duration, maxOut int) (api.RunSandboxedResult, error) {
		<-ctx.Done()
		close(seen)
		return api.RunSandboxedResult{ExitCode: -1}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	body, err := json.Marshal(api.RunSandboxedRequest{Token: sess.HeartbeatToken, Argv: []string{"/usr/bin/true"}})
	if err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	d.handleRunSandboxed(ctx, body)
	select {
	case <-seen:
	default:
		t.Fatal("the runner did not see ctx end")
	}
}

// runChild is the test-side way to run a bare argv through session.RunChild: the
// plan's prefix is the program, so these tests exercise the same route the
// handler uses.
func runChild(ctx context.Context, argv []string, dir string, env []string, timeout time.Duration, maxOut int) (api.RunSandboxedResult, error) {
	return session.RunChild(ctx, &session.RunPlan{Prefix: argv[:1]}, argv[1:], dir, env, timeout, maxOut)
}
