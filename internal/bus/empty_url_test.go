package bus

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

// An empty URL is refused before any dial (marvel#1003). nats.Connect given no
// URL adds its own default, nats://127.0.0.1:4222, which is the managed
// broker's listen address, so an unset address would reach the live broker
// instead of failing with a reason. The test dial guard refuses the empty URL
// first, so nothing is dialed while this runs; what it shows is whether
// connectAdmin got as far as asking the guard. It should not: the refusal comes
// before the guard, the same way the role inbox publisher already refuses a
// publish with no address.
func TestAnEmptyBrokerURLIsRefusedBeforeAnyDial(t *testing.T) {
	for name, dial := range map[string]func(context.Context) error{
		"connectAdmin": func(ctx context.Context) error {
			nc, err := connectAdmin(ctx, "", "u", "p")
			if nc != nil {
				nc.Close()
			}
			return err
		},
		"dialAs": func(ctx context.Context) error { return dialAs(ctx, "", "u", "p") },
	} {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(func() { forgetRefused("") })
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err := dial(ctx)
			if err == nil {
				t.Fatal("a dial with no URL succeeded")
			}
			if !errors.Is(err, errNoAddress) {
				t.Fatalf("an empty URL was refused with %v, want errNoAddress", err)
			}
			if errors.Is(err, errNotStarted) || guardWasAsked("") {
				t.Fatalf("an empty URL reached the dial guard (%v); with the guard off it dials nats.go's default address", err)
			}
		})
	}
}

// guardWasAsked reports whether the dial guard has refused raw in this run.
func guardWasAsked(raw string) bool {
	dialGuardState.Lock()
	defer dialGuardState.Unlock()
	return slices.Contains(dialGuardState.refused, raw)
}
