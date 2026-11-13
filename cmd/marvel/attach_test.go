package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemon"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

func deskRows(panes ...string) []api.Session {
	rows := make([]api.Session, 0, len(panes))
	for _, p := range panes {
		rows = append(rows, api.Session{Name: "s" + p, PaneID: p})
	}
	return rows
}

var (
	localTarget  = attachTarget{Socket: "marvel-ab12cd34"}
	remoteTarget = attachTarget{Socket: "marvel-ff00ff00", Remote: true, Host: "box.example"}
)

// A local desk outside tmux runs the attach in place of the verb, with the
// socket the daemon reported and the pane id the desk column is made from.
func TestPlanAttachLocalOutsideTmuxExecs(t *testing.T) {
	plan, err := planAttach("23", deskRows("%22", "%23"), localTarget, "", false)
	if err != nil {
		t.Fatalf("planAttach: %v", err)
	}
	want := []string{"tmux", "-L", "marvel-ab12cd34", "attach", "-t", "%23"}
	if !reflect.DeepEqual(plan.Argv, want) || !plan.Exec {
		t.Errorf("plan = %+v, want exec of %v", plan, want)
	}
}

// --print never runs anything: the same command, not executed.
func TestPlanAttachPrintDoesNotExec(t *testing.T) {
	plan, err := planAttach("23", deskRows("%23"), localTarget, "", true)
	if err != nil {
		t.Fatalf("planAttach: %v", err)
	}
	if plan.Exec || plan.Argv[3] != "attach" {
		t.Errorf("plan = %+v, want the attach command with Exec false", plan)
	}
}

// A desk typed as it is printed in the pane id ("%23") is the same desk.
func TestPlanAttachAcceptsAPercentPrefix(t *testing.T) {
	plan, err := planAttach("%23", deskRows("%23"), localTarget, "", true)
	if err != nil {
		t.Fatalf("planAttach: %v", err)
	}
	if plan.Argv[len(plan.Argv)-1] != "%23" {
		t.Errorf("argv = %v, want the pane %%23 last", plan.Argv)
	}
}

// Inside marvel's own tmux server the client is switched, not nested.
func TestPlanAttachInsideMarvelTmuxSwitches(t *testing.T) {
	env := "/private/tmp/tmux-501/marvel-ab12cd34,4242,0"
	plan, err := planAttach("23", deskRows("%23"), localTarget, env, false)
	if err != nil {
		t.Fatalf("planAttach: %v", err)
	}
	want := []string{"tmux", "-L", "marvel-ab12cd34", "switch-client", "-t", "%23"}
	if !reflect.DeepEqual(plan.Argv, want) || !plan.Exec {
		t.Errorf("plan = %+v, want exec of %v", plan, want)
	}
}

// Inside any other tmux server the verb opens a new window on that server,
// and the window runs the attach to marvel's server (operator ruling
// lj44u-attach-inside-own-tmux).
func TestPlanAttachInsideOtherTmuxOpensNewWindow(t *testing.T) {
	env := "/private/tmp/tmux-501/default,4242,0"
	plan, err := planAttach("23", deskRows("%23"), localTarget, env, false)
	if err != nil {
		t.Fatalf("planAttach: %v", err)
	}
	want := []string{"tmux", "new-window", "tmux", "-L", "marvel-ab12cd34", "attach", "-t", "%23"}
	if !reflect.DeepEqual(plan.Argv, want) || !plan.Exec {
		t.Errorf("plan = %+v, want exec of %v", plan, want)
	}
	if plan.Note != "" {
		t.Errorf("plan.Note = %q, want no nesting note", plan.Note)
	}
}

// --print inside another tmux still prints the plain attach command and runs
// nothing.
func TestPlanAttachPrintInsideOtherTmuxIsUnchanged(t *testing.T) {
	env := "/private/tmp/tmux-501/default,4242,0"
	plan, err := planAttach("23", deskRows("%23"), localTarget, env, true)
	if err != nil {
		t.Fatalf("planAttach: %v", err)
	}
	want := []string{"tmux", "-L", "marvel-ab12cd34", "attach", "-t", "%23"}
	if !reflect.DeepEqual(plan.Argv, want) || plan.Exec {
		t.Errorf("plan = %+v, want print of %v", plan, want)
	}
}

// scratchTmuxPair starts two scratch tmux servers, marvel's stand-in with two
// windows (the second is the desk, the first stays active) and the operator's
// stand-in with one, and returns a runner per server. TMUX_TMPDIR is moved into
// a scratch directory first: tmux reads it, not TMPDIR, to place its sockets,
// and the outer new-window falls back to the default server when TMUX is gone,
// so both must stay out of the live socket directory.
func scratchTmuxPair(t *testing.T) (mv, op func(args ...string) (string, error), opEnv, pane, active string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Fatal("tmux is not on PATH; CI runner images carry it and a missing one must not read as a pass")
	}
	// A short fixed parent keeps the socket path under the 104-character limit.
	parent := "/tmp"
	if _, err := os.Stat("/private/tmp"); err == nil {
		parent = "/private/tmp"
	}
	dir, err := os.MkdirTemp(parent, "nw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMPDIR", dir)
	// The scratch servers read no configuration file: -f /dev/null below, and
	// HOME is moved so nothing else resolves to the operator's.
	t.Setenv("HOME", dir)
	t.Setenv("MARVEL_SOCKET", "")
	t.Setenv("TMUX", "")
	if err := os.Unsetenv("TMUX"); err != nil {
		t.Fatal(err)
	}
	runner := func(sock string) func(args ...string) (string, error) {
		return func(args ...string) (string, error) {
			out, err := exec.Command("tmux", append([]string{"-f", "/dev/null", "-L", sock}, args...)...).CombinedOutput()
			return strings.TrimSpace(string(out)), err
		}
	}
	mv, op = runner("nwm"), runner("nwo")
	t.Cleanup(func() { _, _ = mv("kill-server"); _, _ = op("kill-server") })
	if out, err := mv("new-session", "-d", "-x", "80", "-y", "24", "sleep 60"); err != nil {
		t.Fatalf("marvel scratch server: %v: %s", err, out)
	}
	if active, err = mv("display-message", "-p", "#{pane_id}"); err != nil {
		t.Fatalf("active pane: %v: %s", err, active)
	}
	// The desk is a second window, created detached so the first stays active.
	if pane, err = mv("new-window", "-d", "-P", "-F", "#{pane_id}", "sleep 60"); err != nil {
		t.Fatalf("desk pane: %v: %s", err, pane)
	}
	if out, err := op("new-session", "-d", "-x", "80", "-y", "24", "sleep 60"); err != nil {
		t.Fatalf("operator scratch server: %v: %s", err, out)
	}
	// A failed inner command leaves its error in the window for a diagnostic.
	if out, err := op("set-option", "-g", "remain-on-exit", "on"); err != nil {
		t.Fatalf("remain-on-exit: %v: %s", err, out)
	}
	if opEnv, err = op("display-message", "-p", "#{socket_path},#{pid},0"); err != nil {
		t.Fatalf("operator env: %v: %s", err, opEnv)
	}
	return mv, op, opEnv, pane, active
}

func windowCount(t *testing.T, run func(args ...string) (string, error)) int {
	t.Helper()
	out, err := run("list-windows", "-a", "-F", "#{window_id}")
	if err != nil {
		t.Fatalf("list-windows: %v: %s", err, out)
	}
	return len(strings.Fields(out))
}

// On a scratch tmux, the plan opens exactly one new window on the operator's
// server, none on marvel's, and that window's client lands on the desk pane,
// which is not the pane the marvel session had active.
func TestAttachNewWindowClientLandsOnPane(t *testing.T) {
	mv, op, opEnv, pane, active := scratchTmuxPair(t)
	if active == pane {
		t.Fatalf("active pane %q and desk pane %q must differ", active, pane)
	}
	opBefore, mvBefore := windowCount(t, op), windowCount(t, mv)
	plan, err := planAttach(strings.TrimPrefix(pane, "%"), []api.Session{{Name: "s", PaneID: pane}}, attachTarget{Socket: "nwm"}, opEnv, false)
	if err != nil || !plan.Exec {
		t.Fatalf("planAttach = %+v, %v, want an exec plan", plan, err)
	}
	cmd := exec.Command(plan.Argv[0], plan.Argv[1:]...)
	cmd.Env = append(os.Environ(), "TMUX="+opEnv)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run %v: %v: %s", plan.Argv, err, out)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, _ := mv("list-clients", "-F", "#{pane_id}")
		if out == pane {
			break
		}
		if time.Now().After(deadline) {
			wins, _ := op("list-windows", "-F", "#{window_index} #{pane_dead}")
			text, _ := op("capture-pane", "-p", "-t", "{end}")
			t.Fatalf("marvel clients' panes = %q, want %q within 5s; operator windows: %s; new window text: %s", out, pane, wins, text)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got := windowCount(t, op); got != opBefore+1 {
		t.Errorf("operator server windows = %d, want %d: exactly one new window", got, opBefore+1)
	}
	if got := windowCount(t, mv); got != mvBefore {
		t.Errorf("marvel server windows = %d, want %d unchanged", got, mvBefore)
	}
}

// A socket whose name merely contains marvel's is another server.
func TestPlanAttachSocketMatchIsExact(t *testing.T) {
	env := "/private/tmp/tmux-501/xmarvel-ab12cd34,4242,0"
	plan, err := planAttach("23", deskRows("%23"), localTarget, env, false)
	if err != nil {
		t.Fatalf("planAttach: %v", err)
	}
	want := []string{"tmux", "new-window", "tmux", "-L", "marvel-ab12cd34", "attach", "-t", "%23"}
	if !reflect.DeepEqual(plan.Argv, want) || !plan.Exec {
		t.Errorf("plan = %+v, a different socket name must not read as marvel's: want exec of %v", plan, want)
	}
}

// A remote desk always prints the ssh command and never runs anything, under
// any flag and from inside any tmux.
func TestPlanAttachRemoteAlwaysPrintsSSH(t *testing.T) {
	for _, tmuxEnv := range []string{"", "/private/tmp/tmux-501/marvel-ff00ff00,1,0", "/private/tmp/tmux-501/default,1,0"} {
		for _, printOnly := range []bool{false, true} {
			plan, err := planAttach("23", deskRows("%23"), remoteTarget, tmuxEnv, printOnly)
			if err != nil {
				t.Fatalf("planAttach: %v", err)
			}
			want := []string{"ssh", "-t", "box.example", "tmux", "-L", "marvel-ff00ff00", "attach", "-t", "%23"}
			if !reflect.DeepEqual(plan.Argv, want) || plan.Exec {
				t.Errorf("TMUX=%q print=%v: plan = %+v, want print of %v", tmuxEnv, printOnly, plan, want)
			}
		}
	}
}

// The remote branch is one function that says it is print-only, so a later
// integration replaces one body.
func TestRemoteAttachIsPrintOnly(t *testing.T) {
	argv, printOnly := remoteAttach("23", "box.example", "marvel-ff00ff00")
	if !printOnly || argv[0] != "ssh" {
		t.Errorf("remoteAttach = %v, %v, want an ssh command and printOnly", argv, printOnly)
	}
}

// A host that reads as an ssh option is refused, since it would be parsed by
// ssh as one.
func TestPlanAttachRefusesAnOptionShapedHost(t *testing.T) {
	tgt := remoteTarget
	tgt.Host = "-oProxyCommand=x"
	if _, err := planAttach("23", deskRows("%23"), tgt, "", true); err == nil {
		t.Error("an option-shaped host was accepted")
	}
}

func TestPlanAttachRefusals(t *testing.T) {
	cases := []struct {
		name string
		desk string
		rows []api.Session
		tgt  attachTarget
		want string
	}{
		{"unknown desk", "99", deskRows("%23"), localTarget, "99"},
		{"two rows", "23", deskRows("%23", "%23"), localTarget, "more than one"},
		{"not a number", "abc", deskRows("%23"), localTarget, "desk"},
		{"empty", "", deskRows("%23"), localTarget, "desk"},
		{"shell text", "23;id", deskRows("%23"), localTarget, "desk"},
		{"daemon reports no socket", "23", deskRows("%23"), attachTarget{}, "upgrade"},
	}
	for _, c := range cases {
		_, err := planAttach(c.desk, c.rows, c.tgt, "", false)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want one containing %q", c.name, err, c.want)
		}
	}
}

func TestQuoteArgv(t *testing.T) {
	got := quoteArgv([]string{"ssh", "-t", "h", "tmux", "-L", "a b", "attach", "-t", "%23", "it's"})
	want := `ssh -t h tmux -L 'a b' attach -t %23 'it'\''s'`
	if got != want {
		t.Errorf("quoteArgv = %q, want %q", got, want)
	}
}

// attachFakeDaemon answers get sessions and daemon.status and records the
// methods it was sent.
func attachFakeDaemon(t *testing.T, rows []api.Session, st daemon.DaemonStatus) (socket string, methods func() []string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "at")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket = filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var (
		mu  sync.Mutex
		got []string
	)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var req daemonrpc.Request
			if err := json.NewDecoder(c).Decode(&req); err == nil {
				mu.Lock()
				got = append(got, req.Method)
				mu.Unlock()
				var data []byte
				switch req.Method {
				case "daemon.status":
					data, _ = json.Marshal(st)
				case "get":
					data, _ = json.Marshal(rows)
				default:
					_ = json.NewEncoder(c).Encode(daemonrpc.Response{Error: "unexpected method " + req.Method})
					_ = c.Close()
					continue
				}
				_ = json.NewEncoder(c).Encode(daemonrpc.Response{Result: data})
			}
			_ = c.Close()
		}
	}()
	return socket, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), got...)
	}
}

func noTmuxEnv(string) string { return "" }

// --print against a real daemon answer: one get and one daemon.status, both
// reads, and the command on stdout with nothing run.
func TestRunAttachPrintReadsOnlyAndPrints(t *testing.T) {
	resolveFixture(t, false)
	socket, methods := attachFakeDaemon(t, deskRows("%23"), daemon.DaemonStatus{TmuxSocket: "marvel-ab12cd34"})
	t.Setenv(config.SocketEnv, socket)

	ran := false
	var out bytes.Buffer
	err := runAttach(&out, "23", true, attachEnv{Getenv: noTmuxEnv, Exec: func([]string) error { ran = true; return nil }})
	if err != nil {
		t.Fatalf("runAttach: %v", err)
	}
	if ran {
		t.Error("--print ran the command")
	}
	if got := strings.TrimSpace(out.String()); got != "tmux -L marvel-ab12cd34 attach -t %23" {
		t.Errorf("stdout = %q", got)
	}
	for _, m := range methods() {
		if m != "get" && m != "daemon.status" {
			t.Errorf("method %q sent; the verb may only read", m)
		}
	}
}

// Without --print, outside tmux, the command is handed to the exec hook.
func TestRunAttachExecsOutsideTmux(t *testing.T) {
	resolveFixture(t, false)
	socket, _ := attachFakeDaemon(t, deskRows("%23"), daemon.DaemonStatus{TmuxSocket: "marvel-ab12cd34"})
	t.Setenv(config.SocketEnv, socket)

	var got []string
	err := runAttach(&bytes.Buffer{}, "23", false, attachEnv{Getenv: noTmuxEnv, Exec: func(argv []string) error { got = argv; return nil }})
	if err != nil {
		t.Fatalf("runAttach: %v", err)
	}
	want := []string{"tmux", "-L", "marvel-ab12cd34", "attach", "-t", "%23"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("exec argv = %v, want %v", got, want)
	}
}

// A daemon that predates tmux_socket gets the upgrade line and no guess.
func TestRunAttachDaemonWithoutSocketRefuses(t *testing.T) {
	resolveFixture(t, false)
	socket, _ := attachFakeDaemon(t, deskRows("%23"), daemon.DaemonStatus{})
	t.Setenv(config.SocketEnv, socket)

	err := runAttach(&bytes.Buffer{}, "23", true, attachEnv{Getenv: noTmuxEnv, Exec: func([]string) error { return nil }})
	if err == nil || !strings.Contains(err.Error(), "upgrade") {
		t.Errorf("err = %v, want the upgrade line", err)
	}
}

// The host comes out of the address the client resolved, without the mrvl
// port (it is not an ssh port) and without a login name (marvel's user is
// not necessarily the ssh one). A local unix socket is not remote.
func TestAttachHost(t *testing.T) {
	cases := []struct {
		addr   string
		host   string
		remote bool
	}{
		{"/private/tmp/x/marvel.sock", "", false},
		{"mrvl://box.example", "box.example", true},
		{"mrvl://ops@box.example:6785", "box.example", true},
		{"ssh://ops@box.example/home/ops/.marvel/run/marvel.sock", "box.example", true},
		{"tcp://10.0.0.5:7000", "10.0.0.5", true},
		{"10.0.0.5:7000", "10.0.0.5", true},
	}
	for _, c := range cases {
		host, remote := attachHost(c.addr)
		if host != c.host || remote != c.remote {
			t.Errorf("attachHost(%q) = %q, %v, want %q, %v", c.addr, host, remote, c.host, c.remote)
		}
	}
}
