package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/bus"
)

// marvel#1463: the post-check passed three different daemon states with one
// line that said no hub was configured. Each state now says what it saw, and an
// adopted bus whose cluster declares a hub is read, not waved through
// (aae-orc-c6o29).

func adoptedUp(now time.Time) bus.Status {
	return bus.Status{
		HubDeclared: true, Ready: true, Leaf: "up",
		LeafObservedAt: now.Format(time.RFC3339), LeafValidUntil: now.Add(time.Minute).Format(time.RFC3339),
	}
}

func TestReexecPostcheckNamesEachNoHubState(t *testing.T) {
	cases := []struct {
		name      string
		world     func() *world
		want      string
		wantNever []string
	}{
		{
			name: "no bus",
			world: func() *world {
				w := newWorld()
				w.leafErr = func(time.Duration) error { return errors.New(noBusReply) }
				return w
			},
			want:      "no bus is configured, so there is no leaf to check",
			wantNever: []string{"no hub"},
		},
		{
			name: "managed bus with no hub",
			world: func() *world {
				w := newWorld()
				w.leaf = func(time.Duration, time.Time) bus.Status { return bus.Status{Managed: true, Leaf: "n/a"} }
				return w
			},
			want:      "the managed bus has no hub configured, so there is no leaf to check",
			wantNever: []string{"no bus"},
		},
		{
			name: "adopted bus with no hub declared",
			world: func() *world {
				w := newWorld()
				w.leaf = func(time.Duration, time.Time) bus.Status {
					return bus.Status{Leaf: "n/a", URL: "nats://127.0.0.1:4222"}
				}
				return w
			},
			want:      "the adopted bus has not been given a hub in this cluster's config, so its leaf is not checked",
			wantNever: []string{"no hub", "no bus"},
		},
	}
	seen := map[string]string{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := run(t, tc.world(), "none")
			if err != nil {
				t.Fatalf("a state with nothing to check failed the check: %v\n%s", err, out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("output does not name the state %q:\n%s", tc.want, out)
			}
			for _, never := range tc.wantNever {
				if strings.Contains(out, never) {
					t.Errorf("output carries %q, which misnames this state:\n%s", never, out)
				}
			}
			seen[tc.name] = out
		})
	}
	if seen["no bus"] != "" && seen["no bus"] == seen["managed bus with no hub"] {
		t.Errorf("two different states print the same line: %q", seen["no bus"])
	}
}

func TestReexecPostcheckAdoptedWithHubDeclaredErrors(t *testing.T) {
	for _, leaf := range []string{"down", "unknown", "detached"} {
		t.Run("leaf "+leaf, func(t *testing.T) {
			w := newWorld()
			w.leaf = func(time.Duration, time.Time) bus.Status {
				return bus.Status{HubDeclared: true, Ready: true, Leaf: leaf, URL: "nats://127.0.0.1:4222"}
			}
			out, err := run(t, w, "none")
			var ce *reexecCheckError
			if !errors.As(err, &ce) {
				t.Fatalf("err = %v, want a reexecCheckError for an adopted bus with a hub declared and its leaf %s\n%s", err, leaf, out)
			}
			if strings.Contains(out, "no hub") {
				t.Errorf("output calls an adopted bus with a declared hub hubless:\n%s", out)
			}
		})
	}
	t.Run("leaf up passes and says so", func(t *testing.T) {
		w := newWorld()
		w.leaf = func(_ time.Duration, now time.Time) bus.Status { return adoptedUp(now) }
		out, err := run(t, w, "none")
		if err != nil {
			t.Fatalf("an adopted bus with its leaf up failed the check: %v\n%s", err, out)
		}
		if !strings.Contains(out, "hub leaf up") {
			t.Errorf("output does not say the leaf is up:\n%s", out)
		}
	})
}

// bus.Status.Adopted means a managed broker the daemon found already running
// after a re-exec (supervisor.go, lifecycle.go), so it is not what marks an
// adopted bus. The daemon's status for a real adopted bus has Managed false and
// no Adopted, which is what the cases above now feed (marvel#1465 review).

func TestReexecPostcheckAdoptedBusDownDoesNotAskForARepush(t *testing.T) {
	w := newWorld()
	w.leaf = func(time.Duration, time.Time) bus.Status {
		return bus.Status{HubDeclared: true, Ready: true, Leaf: "down", URL: "nats://127.0.0.1:4222"}
	}
	out, err := run(t, w, "none")
	var ce *reexecCheckError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want a reexecCheckError\n%s", err, out)
	}
	if strings.Contains(out, "marvel credential put") {
		t.Errorf("output tells the operator to push a leaf credential to a broker marvel does not hold one for:\n%s", out)
	}
	if !strings.Contains(out, "broker owner") {
		t.Errorf("output does not say the broker's owner restores the leaf:\n%s", out)
	}
}

func TestReexecPostcheckAManagedBrokerFoundRunningStillAsksForARepush(t *testing.T) {
	w := newWorld()
	w.leaf = func(time.Duration, time.Time) bus.Status {
		return bus.Status{Managed: true, Adopted: true, Ready: true, HubDeclared: true, Leaf: "down", LeafSource: "dropped-on-adopt"}
	}
	out, err := run(t, w, "none")
	var ce *reexecCheckError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want a reexecCheckError\n%s", err, out)
	}
	if !strings.Contains(out, "marvel credential put bus/leaf") {
		t.Errorf("output does not give the repush command for a managed broker:\n%s", out)
	}
	if strings.Contains(out, "broker owner") {
		t.Errorf("output treats a managed broker found running as one marvel does not manage:\n%s", out)
	}
}

func TestReexecPostcheckManagedBusWithNoHubIsNamedEvenWhenFoundRunning(t *testing.T) {
	w := newWorld()
	w.leaf = func(time.Duration, time.Time) bus.Status {
		return bus.Status{Managed: true, Adopted: true, Leaf: "n/a"}
	}
	out, err := run(t, w, "none")
	if err != nil {
		t.Fatalf("a managed bus with no hub failed the check: %v\n%s", err, out)
	}
	if !strings.Contains(out, "the managed bus has no hub configured") {
		t.Errorf("output does not name a managed bus found running as managed:\n%s", out)
	}
}
