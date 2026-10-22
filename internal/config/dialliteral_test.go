package config

import (
	"strings"
	"testing"
)

func TestDialLiteralWarning(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		addr string
		warn bool
	}{
		{"mrvl literal with port", "mrvl://192.0.2.7:6785", true},
		{"mrvl literal with user and no port", "mrvl://op@192.0.2.7", true},
		{"ssh literal", "ssh://op@192.0.2.7:22", true},
		{"tcp literal", "tcp://192.0.2.7:6785", true},
		{"bare host:port literal", "192.0.2.7:6785", true},
		{"name", "mrvl://kinu:6785", false},
		{"dotted name", "mrvl://op@kinu.example.com:6785", false},
		{"loopback is not cross-host", "mrvl://127.0.0.1:6785", false},
		{"unspecified is not a dial target", "mrvl://0.0.0.0:6785", false},
		{"unix socket", "/run/marvel.sock", false},
		{"empty", "", false},
		{"IPv6 literal is out of scope", "mrvl://[2001:db8::1]:6785", false},
		{"name that starts with digits", "mrvl://192.0.2.7.example.com:6785", false},
	}
	for _, tc := range tests {
		got := DialLiteralWarning(tc.addr)
		if (got != "") != tc.warn {
			t.Errorf("%s: DialLiteralWarning(%q) = %q, want warning=%v", tc.name, tc.addr, got, tc.warn)
		}
		if tc.warn && !strings.Contains(got, "192.0.2.7") {
			t.Errorf("%s: warning %q does not name the literal", tc.name, got)
		}
	}
}

func TestDialLiteralWarningsForConfigNamesTheCluster(t *testing.T) {
	t.Parallel()
	cfg := &Config{Clusters: []Cluster{
		{Name: "lit", Server: "mrvl://192.0.2.7:6785"},
		{Name: "named", Server: "mrvl://kinu:6785"},
		{Name: "sock", Socket: "/run/marvel.sock"},
		{Name: "tcp", Socket: "192.0.2.9:6785"},
	}}
	got := cfg.DialLiteralWarnings()
	if len(got) != 2 {
		t.Fatalf("got %d warnings %q, want 2 (lit and tcp)", len(got), got)
	}
	if !strings.Contains(got[0], `"lit"`) || !strings.Contains(got[1], `"tcp"`) {
		t.Errorf("warnings do not name their clusters: %q", got)
	}
}
