package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

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
	remoteTarget = attachTarget{Socket: "marvel-ff00ff00", Remote: true, Host: "kinu.example"}
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

// Inside any other tmux server a nested attach is the footgun, so the verb
// prints and says why. What to do there is an open operator question.
func TestPlanAttachInsideOtherTmuxPrintsOnly(t *testing.T) {
	env := "/private/tmp/tmux-501/default,4242,0"
	plan, err := planAttach("23", deskRows("%23"), localTarget, env, false)
	if err != nil {
		t.Fatalf("planAttach: %v", err)
	}
	if plan.Exec {
		t.Errorf("plan = %+v, want print only inside another tmux server", plan)
	}
	if plan.Argv[3] != "attach" || plan.Note == "" {
		t.Errorf("plan = %+v, want the attach command and a note naming why", plan)
	}
}

// A socket whose name merely contains marvel's is another server.
func TestPlanAttachSocketMatchIsExact(t *testing.T) {
	env := "/private/tmp/tmux-501/xmarvel-ab12cd34,4242,0"
	plan, err := planAttach("23", deskRows("%23"), localTarget, env, false)
	if err != nil {
		t.Fatalf("planAttach: %v", err)
	}
	if plan.Exec {
		t.Errorf("plan = %+v, a different socket name must not read as marvel's", plan)
	}
}

// A remote desk always prints the ssh command and never runs anything, under
// any flag and from inside any tmux.
func TestPlanAttachRemoteAlwaysPrintsSSH(t *testing.T) {
	for _, tmuxEnv := range []string{"", "/private/tmp/tmux-501/marvel-ff00ff00,1,0"} {
		for _, printOnly := range []bool{false, true} {
			plan, err := planAttach("23", deskRows("%23"), remoteTarget, tmuxEnv, printOnly)
			if err != nil {
				t.Fatalf("planAttach: %v", err)
			}
			want := []string{"ssh", "-t", "kinu.example", "tmux", "-L", "marvel-ff00ff00", "attach", "-t", "%23"}
			if !reflect.DeepEqual(plan.Argv, want) || plan.Exec {
				t.Errorf("TMUX=%q print=%v: plan = %+v, want print of %v", tmuxEnv, printOnly, plan, want)
			}
		}
	}
}

// The remote branch is one function that says it is print-only, so a later
// integration replaces one body.
func TestRemoteAttachIsPrintOnly(t *testing.T) {
	argv, printOnly := remoteAttach("23", "kinu.example", "marvel-ff00ff00")
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
