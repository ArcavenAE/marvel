package main

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/bus"
)

func TestPrintBusStatus(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	printBusStatus(&b, bus.Status{Managed: false, URL: "nats://h:4222"})
	if got := b.String(); !strings.Contains(got, "managed:  false") || !strings.Contains(got, "nats://h:4222") || strings.Contains(got, "pid") {
		t.Errorf("adopted render:\n%s", got)
	}

	b.Reset()
	printBusStatus(&b, bus.Status{
		Managed: true, Ready: false, Leaf: "down", URL: "nats://127.0.0.1:4222", Listen: "127.0.0.1:4222",
		Domain: "kinu", PID: 42, Adopted: true, Restarts: 2, BackoffUntil: "2026-09-15T12:00:00Z", ConfPath: "/x/nats.conf",
	})
	got := b.String()
	for _, want := range []string{"ready:    no", "leaf:     down", "pid:      42 (adopted)", "restarts: 2", "backoff:  until 2026-09-15T12:00:00Z", "conf:     /x/nats.conf"} {
		if !strings.Contains(got, want) {
			t.Errorf("managed render missing %q:\n%s", want, got)
		}
	}
}
