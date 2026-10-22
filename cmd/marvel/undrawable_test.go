package main

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/arcavenae/marvel/internal/admission"
	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemon"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/team"
)

const fffd = "\uFFFD"

// The filter replaces each C0 (tab, LF and CR included), DEL, C1 and format
// character with one U+FFFD, and leaves printable text alone.
func TestFilterReplacesC0C1DELFormat(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", "alpha-1 ok", "alpha-1 ok"},
		{"nul", "a\x00b", "a" + fffd + "b"},
		{"tab", "a\tb", "a" + fffd + "b"},
		{"lf", "a\nb", "a" + fffd + "b"},
		{"cr", "a\rb", "a" + fffd + "b"},
		{"esc", "a\x1b[2Jb", "a" + fffd + "[2Jb"},
		{"del", "a\x7fb", "a" + fffd + "b"},
		{"c1", "a\u0085b\u009bc", "a" + fffd + "b" + fffd + "c"},
		{"bidi", "a\u202Eb\u2066c", "a" + fffd + "b" + fffd + "c"},
		{"zero width", "a\u200Bb\uFEFFc", "a" + fffd + "b" + fffd + "c"},
		{"wide text stays", "\u540D\u524D-\u00E9", "\u540D\u524D-\u00E9"},
	}
	for _, c := range cases {
		got, _ := filterDrawn(c.in)
		if got != c.want {
			t.Errorf("%s: filterDrawn(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// A name holding a newline and a tab draws one row, not a forged second.
func TestNewlineCannotForgeRow(t *testing.T) {
	sessions := []api.Session{{Workspace: "ws", Name: "x\nother:ws/evil\trunning"}}
	cols := []sessionColumn{mustColumn(t, "workspace"), mustColumn(t, "name")}
	out := renderSessionsView(sessions, cols, "", fitOptions{})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("output has %d lines: %q", len(lines), out)
	}
	rows := 0
	for _, l := range lines[1:] {
		if strings.Contains(l, "held undrawable") {
			continue
		}
		rows++
	}
	if rows != 1 {
		t.Errorf("table drew %d rows, want 1:\n%s", rows, out)
	}
}

func mustColumn(t *testing.T, name string) sessionColumn {
	t.Helper()
	c, ok := lookupSessionColumn(name)
	if !ok {
		t.Fatalf("no column %q", name)
	}
	return c
}

// The placeholder is never a separator: whatever goes in, the output holds no
// more ':', '/' or '?' than the input did.
func TestPlaceholderAddsNoSeparator(t *testing.T) {
	// Built as strings, so an invalid byte stays an invalid byte; a []rune
	// conversion would turn "\xff" into U+FFFD before the filter saw it.
	alphabet := []string{"a", "b", ":", "/", "?", "\x00", "\x1b", "\x7f", "\u0085", "\u202E", "\u200B", "\uFF1A", "\u2215", " ", "\xff", "\xc3"}
	rng := rand.New(rand.NewSource(7))
	sawInvalid := false
	for i := 0; i < 500; i++ {
		var b strings.Builder
		for n := rng.Intn(24); n > 0; n-- {
			b.WriteString(alphabet[rng.Intn(len(alphabet))])
		}
		in := b.String()
		sawInvalid = sawInvalid || !utf8.ValidString(in)
		for _, f := range []func(string) (string, []string){filterDrawn, filterKeyPart} {
			out, _ := f(in)
			if !utf8.ValidString(out) {
				t.Fatalf("%q -> %q is not valid UTF-8", in, out)
			}
			for _, sep := range []string{":", "/", "?"} {
				if strings.Count(out, sep) > strings.Count(in, sep) {
					t.Fatalf("%q -> %q gained a %q", in, out, sep)
				}
			}
		}
	}
	if !sawInvalid {
		t.Fatal("no generated input held an invalid byte")
	}
}

// In the workspace and name parts of a key, the separator lookalikes are
// replaced as well; elsewhere they are printable text and stay.
func TestSeparatorLookalikesReplacedInKeyParts(t *testing.T) {
	for _, r := range []rune{0xFF1A, 0x2236, 0xA789, 0xFE55, 0x2215, 0x2044, 0xFF0F, 0x29F8} {
		in := "a" + string(r) + "b"
		if got, _ := filterKeyPart(in); got != "a"+fffd+"b" {
			t.Errorf("filterKeyPart(U+%04X) = %q", r, got)
		}
		if got, _ := filterDrawn(in); got != in {
			t.Errorf("filterDrawn(U+%04X) = %q, want it kept", r, got)
		}
	}
}

// An error text from the daemon is filtered before it reaches the operator.
// The fake answers an unknown method with "unexpected method <name>", so a
// method name holding ESC puts ESC in the error text.
func TestDaemonErrorFiltered(t *testing.T) {
	resolveFixture(t, false)
	socket, _ := attachFakeDaemon(t, nil, daemon.DaemonStatus{})
	t.Setenv(config.SocketEnv, socket)
	resp, err := send(daemonrpc.Request{Method: "m\x1b[2J\u202E"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if resp.Error == "" {
		t.Fatal("the fake daemon returned no error text")
	}
	if strings.ContainsAny(resp.Error, "\x1b\u202E") {
		t.Errorf("error text still holds a control or bidi character: %q", resp.Error)
	}
	if !strings.Contains(resp.Error, fffd) {
		t.Errorf("error text %q holds no placeholder", resp.Error)
	}
}

// Each replaced code point and each invalid byte becomes exactly one U+FFFD,
// so a cell keeps its rune count.
func TestEachReplacementIsOneFFFD(t *testing.T) {
	in := "a\x1bb\xffc\xfe\xfdd\u202E"
	out, held := filterDrawn(in)
	if got, want := utf8.RuneCountInString(out), utf8.RuneCountInString(in); got != want {
		t.Errorf("rune count %d, want %d (out %q)", got, want, out)
	}
	if got := strings.Count(out, fffd); got != 5 {
		t.Errorf("%d placeholders, want 5 (out %q)", got, out)
	}
	if len(held) != 5 {
		t.Errorf("held = %v, want 5 entries", held)
	}
	if !utf8.ValidString(out) {
		t.Errorf("output is not valid UTF-8: %q", out)
	}
}

// A name holding ESC and a workspace holding U+202E give one note line that
// names both code points and points at -o json; clean input gives none.
func TestUndrawableNoteNamesCodePoints(t *testing.T) {
	cols := []sessionColumn{mustColumn(t, "workspace"), mustColumn(t, "name")}
	dirty := []api.Session{
		{Workspace: "ws", Name: "a\x1bb"},
		{Workspace: "w\u202Es", Name: "ok"},
	}
	out := renderSessionsView(dirty, cols, "", fitOptions{})
	want := "2 fields held undrawable characters (U+001B, U+202E); -o json shows them"
	if got := strings.Count(out, want); got != 1 {
		t.Errorf("note appears %d times, want 1:\n%s", got, out)
	}
	clean := renderSessionsView([]api.Session{{Workspace: "ws", Name: "ok"}}, cols, "", fitOptions{})
	if strings.Contains(clean, "undrawable") {
		t.Errorf("clean input drew a note:\n%s", clean)
	}
}

// Event text from the daemon is filtered like table cells.
func TestEventTextFiltered(t *testing.T) {
	var buf bytes.Buffer
	printEventBatch(&buf, []events.Event{{
		Timestamp: time.Date(2026, 10, 22, 1, 2, 3, 0, time.UTC),
		Kind:      "x.y", Session: "s\x1b[2J", Message: "m\nforged\trow", Actor: "a\u202E",
	}}, false)
	out := buf.String()
	if strings.Count(out, "\n") != 1 {
		t.Errorf("event drew %d lines, want 1: %q", strings.Count(out, "\n"), out)
	}
	if strings.ContainsAny(out, "\x1b\u202E") {
		t.Errorf("event row still holds ESC or a bidi control: %q", out)
	}
}

// plainText reports whether out holds no raw control or bidi character and
// does hold the placeholder.
func drawnClean(t *testing.T, what, out string) {
	t.Helper()
	if strings.ContainsAny(out, "\x1b\u202E") {
		t.Errorf("%s still holds ESC or a bidi control: %q", what, out)
	}
	if !strings.Contains(out, fffd) {
		t.Errorf("%s holds no placeholder: %q", what, out)
	}
}

// An error on the live event stream comes from the daemon and does not pass
// through send(); followStream filters it.
func TestEventStreamErrorFiltered(t *testing.T) {
	resolveFixture(t, false)
	dir, err := os.MkdirTemp("", "ev")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		var req daemonrpc.Request
		if json.NewDecoder(c).Decode(&req) == nil {
			_ = json.NewEncoder(c).Encode(daemonrpc.Response{Error: "stream broke \x1b[2J\u202E"})
		}
	}()
	t.Setenv(config.SocketEnv, sock)
	err = followStream(0, func(uint64) json.RawMessage { return json.RawMessage(`{}`) }, func([]events.Event, bool) {})
	if err == nil {
		t.Fatal("followStream returned no error")
	}
	drawnClean(t, "the stream error", err.Error())
}

// The home warning and the build warning are built from fields the daemon
// reported, and are filtered before they print.
func TestDaemonNotesFiltered(t *testing.T) {
	var home bytes.Buffer
	noteDaemonHome(&home, "/tmp/x.sock", "/h\x1b[2J\u202E", "/c")
	if home.Len() == 0 {
		t.Fatal("no home warning for a different home")
	}
	drawnClean(t, "the home warning", home.String())

	buildWarned.Store(false)
	t.Cleanup(func() { buildWarned.Store(false) })
	var build bytes.Buffer
	stamp := stampOf(t, daemon.Build{Version: "9.9.9\x1b[2J\u202E", Channel: "stable", Commit: "abcdef0"})
	noteBuildMismatch(&build, "get", stamp)
	if build.Len() == 0 {
		t.Fatal("no build warning for a different build")
	}
	drawnClean(t, "the build warning", build.String())
}

// Every table that prints daemon fields filters them, and names what it
// replaced in a note line.
func TestOtherTablesFiltered(t *testing.T) {
	esc := "x\x1b[2J\u202E"
	teams, _ := json.Marshal([]api.Team{{Workspace: esc, Name: esc, Roles: []api.Role{{Name: esc, Replicas: 1}}}})
	workspaces, _ := json.Marshal([]api.Workspace{{Name: esc}})
	endpoints, _ := json.Marshal([]api.Endpoint{{Workspace: esc, Name: esc, Team: esc}})
	policies, _ := json.Marshal([]api.Policy{{Workspace: esc, Name: esc, Version: esc}})
	creds, _ := json.Marshal([]api.Credential{{Name: esc, Audience: esc, IssuedBy: esc, Binding: esc}})
	load, _ := json.Marshal([]api.TeamLoad{{Workspace: esc, Team: esc}})
	cases := []struct {
		name string
		out  func() string
	}{
		{"teams", func() string { return captureStdout(t, func() { _ = printTeams(teams) }) }},
		{"workspaces", func() string { return captureStdout(t, func() { _ = printWorkspaces(workspaces) }) }},
		{"endpoints", func() string { return captureStdout(t, func() { _ = printEndpoints(endpoints) }) }},
		{"policies", func() string { return captureStdout(t, func() { _ = printPolicies(policies) }) }},
		{"credentials", func() string { return captureStdout(t, func() { _ = printCredentials(creds) }) }},
		{"plan", func() string {
			return renderPlanTable([]team.RolePlan{{Workspace: esc, Team: esc, Role: esc, HoldDetail: esc}})
		}},
		{"budgets", func() string {
			return renderBudgetTable([]admission.Row{{Workspace: esc, Team: esc, Note: esc}})
		}},
		{"team load", func() string {
			out, err := renderTeamLoad(load, time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			return out
		}},
	}
	for _, c := range cases {
		out := c.out()
		drawnClean(t, c.name, out)
		if !strings.Contains(out, "held undrawable characters (U+001B, U+202E)") {
			t.Errorf("%s names no replaced characters:\n%s", c.name, out)
		}
	}
}

// A GAP line is drawn in the sessions view, so what it replaces is counted in
// the view's one note line.
func TestGapLineReplacementCounted(t *testing.T) {
	cols := []sessionColumn{mustColumn(t, "name"), mustColumn(t, "composer")}
	clean := []api.Session{{Workspace: "ws", Name: "n", ComposerGap: "no pane"}}
	if out := renderSessionsView(clean, cols, "", fitOptions{}); strings.Contains(out, "undrawable") {
		t.Fatalf("a clean GAP line drew a note:\n%s", out)
	}
	dirty := []api.Session{{Workspace: "ws", Name: "n", ComposerGap: "no pane\x1b[2J"}}
	out := renderSessionsView(dirty, cols, "", fitOptions{})
	if got := strings.Count(out, "held undrawable characters (U+001B); -o json shows them"); got != 1 {
		t.Errorf("note appears %d times, want 1:\n%s", got, out)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("the GAP line still holds ESC:\n%s", out)
	}
}
