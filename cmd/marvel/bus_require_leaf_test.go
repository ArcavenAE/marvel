package main

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/bus"
)

// Section 5 of the reexec credential handoff (aae-orc-4h9xp):
// `marvel bus status --require-leaf` exits 0 only when a hub is configured, the
// leaf is attached, and it is up on a valid reading, and otherwise says why.

var requireLeafNow = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

func upReading() bus.Status {
	return bus.Status{
		Managed: true, Ready: true, Leaf: "up", LeafSource: "store",
		LeafObservedAt: requireLeafNow.Add(-10 * time.Second).Format(time.RFC3339),
		LeafValidUntil: requireLeafNow.Add(50 * time.Second).Format(time.RFC3339),
	}
}

func TestRequireLeafPassesOnlyOnAValidUpReading(t *testing.T) {
	ok, why := leafRequirement(upReading(), requireLeafNow)
	if !ok || why != "" {
		t.Fatalf("an up leaf on a valid reading: ok=%v why=%q, want ok and no reason", ok, why)
	}
}

func TestRequireLeafNamesWhyItFails(t *testing.T) {
	stale := upReading()
	stale.LeafValidUntil = requireLeafNow.Add(-time.Second).Format(time.RFC3339)
	noStamp := upReading()
	noStamp.LeafValidUntil = ""
	cases := []struct {
		name string
		st   bus.Status
		want string
	}{
		{"no hub", bus.Status{Managed: true, Leaf: "n/a"}, "no hub"},
		{"an unmanaged bus", bus.Status{Managed: false}, "no hub"},
		{"detached by the operator", bus.Status{Managed: true, Leaf: "detached", LeafSource: "store"}, "detached by operator"},
		{"dropped on adopt", bus.Status{Managed: true, Leaf: "unenrolled", LeafSource: "dropped-on-adopt"}, "dropped on adopt"},
		{"an older sidecar", bus.Status{Managed: true, Leaf: "unenrolled", LeafSource: "unknown"}, "unknown"},
		{"no credential ever pushed", bus.Status{Managed: true, Leaf: "unenrolled"}, "not enrolled"},
		{"not polled yet", bus.Status{Managed: true, Leaf: "unknown", LeafSource: "store"}, "unknown"},
		{"a stale up reading", stale, "unknown"},
		{"an up reading with no validity stamp", noStamp, "unknown"},
		{"down", bus.Status{Managed: true, Leaf: "down", LeafSource: "store", LeafValidUntil: requireLeafNow.Add(time.Minute).Format(time.RFC3339)}, "down"},
	}
	for _, c := range cases {
		ok, why := leafRequirement(c.st, requireLeafNow)
		if ok || why != c.want {
			t.Errorf("%s: ok=%v why=%q, want a failure that says %q", c.name, ok, why, c.want)
		}
	}
}

func TestBusStatusDeclaresRequireLeaf(t *testing.T) {
	for _, c := range busCmd().Commands() {
		if c.Name() != "status" {
			continue
		}
		f := c.Flags().Lookup("require-leaf")
		if f == nil {
			t.Fatal("bus status has no --require-leaf flag")
		}
		if f.DefValue != "false" {
			t.Errorf("--require-leaf defaults to %q, want false", f.DefValue)
		}
		return
	}
	t.Fatal("no bus status command")
}

func TestPrintBusStatusShowsALeafSourceThatIsNotTheStore(t *testing.T) {
	var b strings.Builder
	printBusStatus(&b, bus.Status{Managed: true, Leaf: "unenrolled", LeafSource: "dropped-on-adopt", URL: "u", Listen: "l", Domain: "d"})
	got := b.String()
	for _, want := range []string{"leaf-src: dropped-on-adopt", "marvel credential put bus/leaf"} {
		if !strings.Contains(got, want) {
			t.Errorf("render lacks %q:\n%s", want, got)
		}
	}

	b.Reset()
	printBusStatus(&b, bus.Status{Managed: true, Leaf: "unenrolled", LeafSource: "unknown", URL: "u", Listen: "l", Domain: "d"})
	if got := b.String(); !strings.Contains(got, "warning:") || !strings.Contains(got, "leaf-src: unknown") {
		t.Errorf("an unknown leaf source must print as a warning row:\n%s", got)
	}

	b.Reset()
	printBusStatus(&b, bus.Status{Managed: true, Leaf: "up", LeafSource: "store", URL: "u", Listen: "l", Domain: "d"})
	if got := b.String(); strings.Contains(got, "leaf-src") {
		t.Errorf("the ordinary store source adds no row:\n%s", got)
	}
}
