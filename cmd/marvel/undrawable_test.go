package main

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemon"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/events"
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
	alphabet := []rune("ab:/?\x00\x1b\x7f\u0085\u202E\u200B\uFF1A\u2215 \xff")
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 500; i++ {
		var b strings.Builder
		for n := rng.Intn(24); n > 0; n-- {
			b.WriteRune(alphabet[rng.Intn(len(alphabet))])
		}
		in := b.String()
		for _, f := range []func(string) (string, []string){filterDrawn, filterKeyPart} {
			out, _ := f(in)
			for _, sep := range []string{":", "/", "?"} {
				if strings.Count(out, sep) > strings.Count(in, sep) {
					t.Fatalf("%q -> %q gained a %q", in, out, sep)
				}
			}
		}
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
	if got := strings.Count(out, fffd); got != 6 {
		t.Errorf("%d placeholders, want 6 (out %q)", got, out)
	}
	if len(held) != 6 {
		t.Errorf("held = %v, want 6 entries", held)
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
