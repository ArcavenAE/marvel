package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemon"
)

// keyedRows builds session rows from "workspace/name:pane" specs, the pane
// being the pane id as the row carries it ("%23", "", "%x"). deskRows sets no
// workspace, so it cannot stand in for a key.
func keyedRows(specs ...string) []api.Session {
	rows := make([]api.Session, 0, len(specs))
	for _, spec := range specs {
		key, pane, _ := strings.Cut(spec, ":")
		ws, name, _ := strings.Cut(key, "/")
		rows = append(rows, api.Session{Workspace: ws, Name: name, PaneID: pane})
	}
	return rows
}

const (
	msgNoDeskSuffix = "; if you meant a session, pass its key (workspace/name) from `marvel get sessions`"
	msgEmpty        = "attach needs a desk number, a session key or a session name"
)

func TestResolveAttachArgResolves(t *testing.T) {
	rows := keyedRows("aae/builder:%23", "aae/other:%24", "ops/solo:%31")
	cases := []struct {
		name, arg string
		want      attachResolved
	}{
		{"a desk", "23", attachResolved{Desk: "23", Key: "aae/builder"}},
		{"a desk with a percent sign", "%24", attachResolved{Desk: "24", Key: "aae/other"}},
		{"a session key", "ops/solo", attachResolved{Desk: "31", Key: "ops/solo"}},
		{"a unique bare name", "solo", attachResolved{Desk: "31", Key: "ops/solo"}},
		{"a unique bare name that exists in one workspace only", "builder", attachResolved{Desk: "23", Key: "aae/builder"}},
	}
	for _, c := range cases {
		got, err := resolveAttachArg(c.arg, rows)
		if err != nil || got != c.want {
			t.Errorf("%s: resolve(%q) = %+v, %v; want %+v", c.name, c.arg, got, err, c.want)
		}
	}
}

func TestResolveAttachArgRefusals(t *testing.T) {
	cases := []struct {
		name, arg string
		rows      []api.Session
		want      string
	}{
		{"unknown desk", "99", keyedRows("aae/a:%23"), "no session has desk 99 on this cluster" + msgNoDeskSuffix},
		{"unknown desk with a percent sign", "%99", keyedRows("aae/a:%23"), "no session has desk 99 on this cluster" + msgNoDeskSuffix},
		{"unknown key", "aae/x", keyedRows("aae/a:%23"), `no session has key "aae/x" on this cluster`},
		{"unknown name", "x", keyedRows("aae/a:%23"), "no session is named \"x\" on this cluster; pass workspace/name, or a desk from `marvel get sessions`"},
		{"a name in two workspaces", "x", keyedRows("ops/x:%40", "aae/x:%12"), `"x" names 2 sessions on this cluster; pass one of: aae/x (desk 12), ops/x (desk 40)`},
		{"the same two rows the other way round", "x", keyedRows("aae/x:%12", "ops/x:%40"), `"x" names 2 sessions on this cluster; pass one of: aae/x (desk 12), ops/x (desk 40)`},
		{"a name in two workspaces, one of them with no desk", "x", keyedRows("aae/x:%12", "ops/x:"), `"x" names 2 sessions on this cluster; pass one of: aae/x (desk 12), ops/x (no desk)`},
		{
			"a desk and a name", "23", keyedRows("aae/aae-orc-builder-g55-0:%23", "aae/23:%40"),
			`"23" is both desk 23 (aae/aae-orc-builder-g55-0) and a session named 23 (aae/23, desk 40) on this cluster; pass %23 for the desk or aae/23 for the session`,
		},
		{
			"a desk and a name in two workspaces", "23", keyedRows("aae/a:%23", "aae/23:%40", "ops/23:%41"),
			`"23" is both desk 23 (aae/a) and the name of 2 sessions (aae/23, desk 40; ops/23, desk 41) on this cluster; pass %23 for the desk or a key such as aae/23 for a session`,
		},
		{"digits with only a name", "23", keyedRows("aae/23:%40"), "no session has desk 23 on this cluster" + msgNoDeskSuffix},
		{"two rows on one desk", "23", keyedRows("aae/a:%23", "aae/b:%23"), "desk 23 names more than one session row on this cluster"},
		{"a key whose row has no pane id", "aae/a", keyedRows("aae/a:"), `aae/a has no desk (its pane id is ""): there is no pane to attach to`},
		{"a name whose pane id is a bare percent", "a", keyedRows("aae/a:%"), `aae/a has no desk (its pane id is "%"): there is no pane to attach to`},
		{"a key whose pane id is not digits", "aae/a", keyedRows("aae/a:%x"), `aae/a has no desk (its pane id is "%x"): there is no pane to attach to`},
		{"a name whose pane id has no percent sign", "a", keyedRows("aae/a:23"), `aae/a has no desk (its pane id is "23"): there is no pane to attach to`},
		{"empty", "", keyedRows("aae/a:%23"), msgEmpty},
		{"a slash alone", "/", keyedRows("aae/a:%23"), msgEmpty},
		{"white space", "  ", keyedRows("aae/a:%23"), msgEmpty},
	}
	for _, c := range cases {
		got, err := resolveAttachArg(c.arg, c.rows)
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: resolve(%q) error = %v, want exactly %q", c.name, c.arg, err, c.want)
		}
		if got != (attachResolved{}) {
			t.Errorf("%s: resolve(%q) returned %+v alongside the refusal", c.name, c.arg, got)
		}
	}
}

// A percent sign says desk and a slash says key, so neither is ambiguous with a
// session whose name is the same digits.
func TestResolveAttachArgDisambiguatesADeskFromAnAllDigitName(t *testing.T) {
	rows := keyedRows("aae/aae-orc-builder-g55-0:%23", "aae/23:%40")
	if got, err := resolveAttachArg("%23", rows); err != nil || got != (attachResolved{Desk: "23", Key: "aae/aae-orc-builder-g55-0"}) {
		t.Errorf("%%23 = %+v, %v; want the desk", got, err)
	}
	if got, err := resolveAttachArg("aae/23", rows); err != nil || got != (attachResolved{Desk: "40", Key: "aae/23"}) {
		t.Errorf("aae/23 = %+v, %v; want the session named 23", got, err)
	}
}

// Text that could reach a shell resolves nothing, so no argv is built from it.
func TestResolveAttachArgBuildsNothingFromShellText(t *testing.T) {
	rows := keyedRows("aae/a:%23")
	// The control: the same row resolves when it is named properly.
	if got, err := resolveAttachArg("aae/a", rows); err != nil || got != (attachResolved{Desk: "23", Key: "aae/a"}) {
		t.Fatalf("control: resolve(aae/a) = %+v, %v", got, err)
	}
	for _, arg := range []string{"a/b;id", "a;id", "$(id)", "aae/a;id", "23;id", "-t", "aae/a b"} {
		got, err := resolveAttachArg(arg, rows)
		if err == nil || got != (attachResolved{}) {
			t.Errorf("resolve(%q) = %+v, %v; want a refusal", arg, got, err)
		}
	}
}

// What the verb does with an argument, end to end against a fake daemon.
func runAttachWith(t *testing.T, arg string, printOnly bool, rows []api.Session, st daemon.DaemonStatus) (stdout, stderr string, argv []string, err error) {
	t.Helper()
	resolveFixture(t, false)
	socket, _ := attachFakeDaemon(t, rows, st)
	t.Setenv(config.SocketEnv, socket)
	var out, errOut bytes.Buffer
	err = runAttach(&out, arg, printOnly, attachEnv{
		Getenv: noTmuxEnv,
		Exec:   func(a []string) error { argv = a; return nil },
		Stderr: &errOut,
	})
	return out.String(), errOut.String(), argv, err
}

var attachRows = keyedRows("aae/builder:%23", "aae/solo:%31")

// --print gives one pasteable line and the same argv for every form.
func TestRunAttachPrintIsTheSameForEveryForm(t *testing.T) {
	st := daemon.DaemonStatus{TmuxSocket: "marvel-ab12cd34"}
	want := "tmux -L marvel-ab12cd34 attach -t %31\n"
	for _, arg := range []string{"31", "%31", "aae/solo", "solo"} {
		out, _, argv, err := runAttachWith(t, arg, true, attachRows, st)
		if err != nil || out != want || argv != nil {
			t.Errorf("--print %q: stdout %q, ran %v, err %v; want %q and nothing run", arg, out, argv, err, want)
		}
	}
}

// Every form, a desk included, says on stderr which session it opened, once,
// and the refusals say nothing there: the line is written only after the
// resolution has passed every check.
func TestRunAttachEchoesOnStderrForEveryFormAndOnlyOnSuccess(t *testing.T) {
	st := daemon.DaemonStatus{TmuxSocket: "marvel-ab12cd34"}
	for arg, want := range map[string]string{
		"31": "aae/solo is desk 31\n", "%31": "aae/solo is desk 31\n", "aae/solo": "aae/solo is desk 31\n", "solo": "aae/solo is desk 31\n",
	} {
		out, errOut, argv, err := runAttachWith(t, arg, false, attachRows, st)
		if err != nil || errOut != want {
			t.Errorf("%q: stderr %q, err %v; want %q", arg, errOut, err, want)
		}
		if out != "" || !reflect.DeepEqual(argv, []string{"tmux", "-L", "marvel-ab12cd34", "attach", "-t", "%31"}) {
			t.Errorf("%q: stdout %q, argv %v", arg, out, argv)
		}
	}
	collision := keyedRows("aae/builder:%23", "aae/23:%40")
	for _, arg := range []string{"23", "nosuch", "aae/nosuch", ""} {
		_, errOut, argv, err := runAttachWith(t, arg, false, collision, st)
		if err == nil || errOut != "" || argv != nil {
			t.Errorf("%q: err %v, stderr %q, ran %v; want a refusal with nothing on stderr and nothing run", arg, err, errOut, argv)
		}
	}
}

// A daemon that does not report its socket refuses after resolution, and the
// echo does not name a seat that did not open.
func TestRunAttachSaysNothingWhenThePlanRefuses(t *testing.T) {
	_, errOut, argv, err := runAttachWith(t, "aae/solo", false, attachRows, daemon.DaemonStatus{})
	if err == nil || !strings.Contains(err.Error(), "upgrade") || errOut != "" || argv != nil {
		t.Errorf("err %v, stderr %q, ran %v; want the upgrade refusal and nothing else", err, errOut, argv)
	}
}

// On a remote cluster every form ends in the same print-only ssh line, with the
// desk the argument resolved to and nothing of what was typed.
func TestResolvedFormsPlanTheSameRemoteSSH(t *testing.T) {
	want := []string{"ssh", "-t", "box.example", "tmux", "-L", "marvel-ff00ff00", "attach", "-t", "%31"}
	for _, arg := range []string{"31", "%31", "aae/solo", "solo"} {
		res, err := resolveAttachArg(arg, attachRows)
		if err != nil {
			t.Fatalf("resolve(%q): %v", arg, err)
		}
		plan, err := planAttach(res.Desk, attachRows, remoteTarget, "", false)
		if err != nil || plan.Exec || !reflect.DeepEqual(plan.Argv, want) {
			t.Errorf("%q: plan %+v, err %v; want the print-only %v", arg, plan, err, want)
		}
	}
}
