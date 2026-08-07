package config

import (
	"strings"
	"testing"
)

func TestDaemonHomeWarning(t *testing.T) {
	t.Parallel()

	const (
		mine   = "/Users/op/.marvel"
		theirs = "/Users/other/.marvel"
		sock   = "/Users/op/.marvel/run/marvel.sock"
	)

	tests := []struct {
		name       string
		addr       string
		daemonHome string
		clientHome string
		wantWarn   bool
	}{
		{
			name:       "mismatch on a local socket warns",
			addr:       sock,
			daemonHome: theirs,
			clientHome: mine,
			wantWarn:   true,
		},
		{
			name:       "matching homes stay quiet",
			addr:       sock,
			daemonHome: mine,
			clientHome: mine,
		},
		{
			name:       "remote mrvl cluster is expected to differ",
			addr:       "mrvl://other-host",
			daemonHome: theirs,
			clientHome: mine,
		},
		{
			name:       "remote ssh cluster is expected to differ",
			addr:       "ssh://op@other-host/home/op/.marvel/run/marvel.sock",
			daemonHome: theirs,
			clientHome: mine,
		},
		{
			name:       "explicit tcp is expected to differ",
			addr:       "tcp://127.0.0.1:9090",
			daemonHome: theirs,
			clientHome: mine,
		},
		{
			name:       "bare host:port is expected to differ",
			addr:       "127.0.0.1:9090",
			daemonHome: theirs,
			clientHome: mine,
		},
		{
			name:       "daemon predating the field says nothing",
			addr:       sock,
			daemonHome: "",
			clientHome: mine,
		},
		{
			name:       "no client home leaves nothing to compare",
			addr:       sock,
			daemonHome: theirs,
			clientHome: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := DaemonHomeWarning(tt.addr, tt.daemonHome, tt.clientHome)
			if tt.wantWarn && got == "" {
				t.Fatalf("expected a warning for addr %q, daemon %q, client %q",
					tt.addr, tt.daemonHome, tt.clientHome)
			}
			if !tt.wantWarn && got != "" {
				t.Fatalf("expected no warning, got %q", got)
			}
		})
	}
}

// The warning has to be actionable on its own: an operator reading one
// line of stderr needs both homes and the way out.
func TestDaemonHomeWarningNamesBothHomesAndTheOverride(t *testing.T) {
	t.Parallel()

	const (
		mine   = "/Users/op/.marvel"
		theirs = "/Users/other/.marvel"
		sock   = "/Users/op/.marvel/run/marvel.sock"
	)

	w := DaemonHomeWarning(sock, theirs, mine)
	if w == "" {
		t.Fatal("expected a warning")
	}
	for _, want := range []string{theirs, mine, sock, "--socket", SocketEnv} {
		if !strings.Contains(w, want) {
			t.Errorf("warning does not mention %q: %s", want, w)
		}
	}
}

func TestIsLocalSocket(t *testing.T) {
	t.Parallel()

	tests := []struct {
		addr string
		want bool
	}{
		{"/Users/op/.marvel/run/marvel.sock", true},
		{"/tmp/marvel.sock", true},
		{"mrvl://host", false},
		{"mrvl://op@host:6785", false},
		{"ssh://op@host/run/marvel.sock", false},
		{"tcp://host:9090", false},
		{"host:9090", false},
		{":9090", false},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			t.Parallel()
			if got := isLocalSocket(tt.addr); got != tt.want {
				t.Errorf("isLocalSocket(%q) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}
