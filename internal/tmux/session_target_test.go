package tmux

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// tmux resolves a bare target by prefix, and by more than one kind of name: a
// session name matches the start of another session's name, and a window name
// can be read as the session (marvel#737). The driver names a session with
// `=<name>` where the command takes a session, and `=<name>:` where it takes a
// window or pane, so the name is matched whole and only as a session.

func paneSession(t *testing.T, d *Driver, pane string) string {
	t.Helper()
	out, err := d.cmd("display-message", "-p", "-t", pane, "#{session_name}").Output()
	if err != nil {
		t.Fatalf("session of %s: %v", pane, err)
	}
	return strings.TrimSpace(string(out))
}

// A window whose name starts with the session's name took the new window's
// target: `new-window -t s` read s as the window "sh" and failed, and in the
// other direction `list-panes -s` and `set-option` acted on another session.
func TestNewPaneAtIgnoresAWindowNamePrefix(t *testing.T) {
	t.Run("the shell's window name starts with the session name", func(t *testing.T) {
		d := utf8Driver(t)
		t.Setenv("SHELL", "/bin/sh")
		if err := d.NewSession("s"); err != nil {
			t.Fatalf("new session: %v", err)
		}
		pane, err := d.NewPane("s", "sleep 300", "seat", nil, false)
		if err != nil {
			t.Fatalf("new pane in a session whose first window is named sh: %v", err)
		}
		if got := paneSession(t, d, pane); got != "s" {
			t.Errorf("pane landed in %q, want s", got)
		}
	})
	t.Run("a seat window named after the workspace session", func(t *testing.T) {
		d := utf8Driver(t)
		if out, err := d.cmd("new-session", "-d", "-s", "marvel-ws", "-n", "marvel-ws-seat0").CombinedOutput(); err != nil {
			t.Fatalf("new-session: %s: %v", out, err)
		}
		pane, err := d.NewPane("marvel-ws", "sleep 300", "seat1", nil, false)
		if err != nil {
			t.Fatalf("new pane in marvel-ws: %v", err)
		}
		if got := paneSession(t, d, pane); got != "marvel-ws" {
			t.Errorf("pane landed in %q, want marvel-ws", got)
		}
	})
	t.Run("an exact window name still works", func(t *testing.T) {
		d := utf8Driver(t)
		if err := d.NewSession("x"); err != nil {
			t.Fatalf("new session: %v", err)
		}
		pane, err := d.NewPane("x", "sleep 300", "x", nil, false)
		if err != nil {
			t.Fatalf("new pane whose window is named like its session: %v", err)
		}
		if got := paneSession(t, d, pane); got != "x" {
			t.Errorf("pane landed in %q, want x", got)
		}
		st, err := d.PaneStatus(pane)
		if err != nil || !st.Exists || !st.Created {
			t.Errorf("PaneStatus = %+v, %v; want the pane marvel made", st, err)
		}
	})
}

// Another session's window named sx must not capture commands aimed at s.
// tmux reads a bare s as the start of that window's name when o is the
// current session, measured on tmux 3.7b and 3.4.
func TestSessionCommandsIgnoreAnotherSessionsWindowName(t *testing.T) {
	d := utf8Driver(t)
	if err := d.NewSession("s"); err != nil {
		t.Fatalf("new session s: %v", err)
	}
	pane, err := d.NewPane("s", "sleep 300", "seat", nil, false)
	if err != nil {
		t.Fatalf("new pane: %v", err)
	}
	// o is made last, so it is the session tmux treats as current, which is
	// when it reads a bare s as the start of o's window name.
	if out, err := d.cmd("new-session", "-d", "-s", "o", "-n", "sx").CombinedOutput(); err != nil {
		t.Fatalf("new-session o: %s: %v", out, err)
	}
	want := strconv.Itoa(DefaultHistoryLimit)
	if got, err := d.ShowOption("s", "history-limit"); err != nil || got != want {
		t.Errorf("history-limit of s = %q, %v; want %s (the raise went to s)", got, err, want)
	}
	if got, err := d.ShowOption("o", "history-limit"); err != nil || got == want {
		t.Errorf("history-limit of o = %q, %v; the raise for s must not reach o", got, err)
	}
	panes, err := d.ListPanes("s")
	if err != nil {
		t.Fatalf("list panes: %v", err)
	}
	var ids []string
	for _, p := range panes {
		ids = append(ids, p.ID)
		if got := paneSession(t, d, p.ID); got != "s" {
			t.Errorf("ListPanes(s) returned %s from session %q", p.ID, got)
		}
	}
	if !slices.Contains(ids, pane) {
		t.Errorf("ListPanes(s) = %v, want it to hold %s", ids, pane)
	}
}

// With only sab present, the name s is absent. A bare `-t s` matched sab by
// prefix: has-session said yes, and kill-session killed it.
func TestAbsentSessionIsNotReadAsALongerName(t *testing.T) {
	d := utf8Driver(t)
	if err := d.NewSession("sab"); err != nil {
		t.Fatalf("new session: %v", err)
	}
	pane, err := d.NewPane("sab", "sleep 300", "seat", nil, false)
	if err != nil {
		t.Fatalf("new pane: %v", err)
	}
	if ok, err := d.sessionExists("s"); err != nil || ok {
		t.Errorf("sessionExists(s) = %v, %v; want false, nil with only sab present", ok, err)
	}
	if err := d.KillSession("s"); err != nil {
		t.Errorf("KillSession(s): %v", err)
	}
	sessions, err := d.ListSessions()
	if err != nil || !slices.Contains(sessions, "sab") {
		t.Fatalf("sessions = %v, %v; killing the absent s must leave sab running", sessions, err)
	}
	if !d.HasPane(pane) {
		t.Error("sab's pane was killed along with the absent s")
	}
	if _, err := d.NewPane("s", "sleep 300", "seat", nil, false); err == nil {
		t.Error("NewPane in the absent session s succeeded, in sab")
	}
	if panes, err := d.ListPanes("s"); err == nil {
		t.Errorf("ListPanes(s) = %v, nil; want an error for the absent session", panes)
	}
	if _, err := d.ShowOption("s", "history-limit"); err == nil {
		t.Error("ShowOption(s) read sab's option")
	}
}

// Workspaces aae and aae2 give the sessions marvel-aae and marvel-aae2. With
// marvel-aae absent, nothing aimed at it may reach marvel-aae2.
func TestAbsentWorkspaceSessionLeavesALongerOneRunning(t *testing.T) {
	d := utf8Driver(t)
	if err := d.NewSession("marvel-aae2"); err != nil {
		t.Fatalf("new session: %v", err)
	}
	pane, err := d.NewPane("marvel-aae2", "sleep 300", "seat", nil, false)
	if err != nil {
		t.Fatalf("new pane: %v", err)
	}
	if ok, err := d.sessionExists("marvel-aae"); err != nil || ok {
		t.Errorf("sessionExists(marvel-aae) = %v, %v; want false, nil", ok, err)
	}
	if err := d.KillSession("marvel-aae"); err != nil {
		t.Errorf("KillSession(marvel-aae): %v", err)
	}
	sessions, err := d.ListSessions()
	if err != nil || !slices.Contains(sessions, "marvel-aae2") {
		t.Fatalf("sessions = %v, %v; marvel-aae2 must still run", sessions, err)
	}
	if !d.HasPane(pane) {
		t.Error("marvel-aae2's pane did not survive the kill of marvel-aae")
	}
}

// The #726 classifier reads tmux's not-found wording for a missing session, so
// a missing `=name` must still say "can't find session". Measured byte for
// byte as `can't find session: <name>` on tmux 3.7b (macOS) and 3.4 (Linux).
func TestHasSessionOnAMissingExactTargetKeepsTheNotFoundWording(t *testing.T) {
	d := utf8Driver(t)
	if err := d.NewSession("present"); err != nil {
		t.Fatalf("new session: %v", err)
	}
	out, err := d.cmd("has-session", "-t", "=missing").CombinedOutput()
	if err == nil {
		t.Fatal("has-session on a missing exact name succeeded")
	}
	if got := strings.TrimSpace(string(out)); got != "can't find session: missing" {
		t.Errorf("has-session text = %q, want %q", got, "can't find session: missing")
	}
	if ok, err := d.sessionExists("missing"); err != nil || ok {
		t.Errorf("sessionExists(missing) = %v, %v; want false, nil", ok, err)
	}
}

// Fails if driver.go passes a bare session name to -t: every -t in it must
// take a pane id or a name made by sessionTarget or sessionScope. This is the
// shape of TestNoNewRouteIntoTheDaemonsOwnSocket: a scan of the source, so a
// new call site cannot bring the bare form back unseen.
func TestDriverNeverPassesABareSessionNameToDashT(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "driver.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	allowedIdents := map[string]bool{"paneID": true}
	allowedCalls := map[string]bool{"sessionTarget": true, "sessionScope": true}
	check := func(list []ast.Expr) {
		for i, e := range list {
			lit, ok := e.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || lit.Value != `"-t"` {
				continue
			}
			if i+1 >= len(list) {
				t.Errorf("%s: -t with no target", fset.Position(lit.Pos()))
				continue
			}
			switch arg := list[i+1].(type) {
			case *ast.Ident:
				if !allowedIdents[arg.Name] {
					t.Errorf("%s: -t takes the bare %s; use sessionTarget or sessionScope for a session", fset.Position(arg.Pos()), arg.Name)
				}
			case *ast.CallExpr:
				if id, ok := arg.Fun.(*ast.Ident); !ok || !allowedCalls[id.Name] {
					t.Errorf("%s: -t takes a call that is not sessionTarget or sessionScope", fset.Position(arg.Pos()))
				}
			default:
				t.Errorf("%s: -t takes a form this guard does not know", fset.Position(arg.Pos()))
			}
		}
	}
	seen := 0
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			// Only the calls that build a tmux argument list: d.cmd(...) and
			// append(args, ...). A `ps -t <tty>` is not tmux.
			isCmd := false
			switch fun := x.Fun.(type) {
			case *ast.SelectorExpr:
				isCmd = fun.Sel.Name == "cmd"
			case *ast.Ident:
				isCmd = fun.Name == "append"
			}
			if isCmd {
				check(x.Args)
				seen += len(x.Args)
			}
		case *ast.CompositeLit:
			check(x.Elts)
			seen += len(x.Elts)
		}
		return true
	})
	if seen == 0 {
		t.Fatal("the scan saw no arguments, so it checked nothing")
	}
}
