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

func TestPrintBusStatusRecordFields(t *testing.T) {
	var b strings.Builder
	printBusStatus(&b, bus.Status{
		Managed: true, Ready: true, Leaf: "n/a", URL: "nats://127.0.0.1:4222", Listen: "127.0.0.1:4222", Domain: "kinu",
		Class: "message-bus", Provider: "nats-server", Mode: "managed", CallerIdentity: "api-key",
		Version: "v2.14.6", Seat: "aae-orc/ops", SeatPassFile: "/s/nats/director.pass", PID: 7, ConfPath: "/x",
	})
	got := b.String()
	for _, want := range []string{"class:    message-bus\n", "provider: nats-server\n", "mode:     managed\n", "caller:   api-key\n", "version:  v2.14.6\n", "seat:     aae-orc/ops (user director, password at /s/nats/director.pass)\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("status lacks %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "class:") > strings.Index(got, "managed:") {
		t.Errorf("record fields should lead:\n%s", got)
	}
	b.Reset()
	printBusStatus(&b, bus.Status{Managed: false, URL: "nats://h:1", Class: "message-bus", Provider: "nats-server", Mode: "external"})
	if got := b.String(); !strings.Contains(got, "mode:     external\n") || strings.Contains(got, "version") || strings.Contains(got, "seat") {
		t.Errorf("external render:\n%s", got)
	}
}

func TestPrintBusStatusStructuralReading(t *testing.T) {
	var b strings.Builder
	printBusStatus(&b, bus.Status{
		Managed: true, Ready: false, Leaf: "n/a", URL: "nats://127.0.0.1:4222", Listen: "127.0.0.1:4222", Domain: "kinu", PID: 7, ConfPath: "/x",
		Structure: &bus.StructureStatus{Provisioned: false, Authorized: true, TLS: false, Missing: []string{"AGENT_INBOX"}},
	})
	got := b.String()
	for _, want := range []string{"ready:    no\n", "provisioned: no (missing AGENT_INBOX)\n", "authorized:  yes\n", "tls:         no\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("status lacks %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "provisioned:") < strings.Index(got, "ready:") {
		t.Errorf("the structural lines should follow ready, which they explain:\n%s", got)
	}
	b.Reset()
	printBusStatus(&b, bus.Status{Managed: true, Ready: true, Leaf: "n/a", PID: 7})
	if strings.Contains(b.String(), "provisioned") {
		t.Error("structural lines printed with no reading")
	}
}
