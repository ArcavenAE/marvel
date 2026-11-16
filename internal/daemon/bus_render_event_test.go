package daemon

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/bus"
	"github.com/arcavenae/marvel/internal/events"
)

// A render that changed the files and whose reload the broker refused is not
// "not rendered": the files are written and the broker kept its old
// configuration (aae-orc-mqvn5). The ring says which.

func renderEvents(ring *events.Ring) []events.Event {
	return ring.Snapshot(events.Filter{Kind: events.KindBusRendered}, 0)
}

func TestEmitBusRenderNamesARefusedReloadAsSuch(t *testing.T) {
	t.Parallel()
	ring := events.NewRing(8)
	d := &Daemon{events: ring}
	err := fmt.Errorf("reload broker after rewrite: %w", fmt.Errorf("bus: nats-server pid 7: %w", bus.ErrReloadRefused))
	d.emitBusRender("apply", true, err)
	evs := renderEvents(ring)
	if len(evs) != 1 {
		t.Fatalf("%d bus.rendered events, want 1", len(evs))
	}
	msg := evs[0].Message
	if !strings.Contains(msg, "bus config rendered, but the broker refused the reload") {
		t.Errorf("message %q does not say the config was rendered and the reload refused", msg)
	}
	if strings.Contains(msg, "not rendered") {
		t.Errorf("message %q still says not rendered", msg)
	}
	if evs[0].Severity != events.SeverityWarning {
		t.Errorf("severity = %v, want warning", evs[0].Severity)
	}
}

// Control: any other render failure still reads as not rendered, and so does a
// refusal-shaped text that is not the sentinel.
func TestEmitBusRenderStillSaysNotRenderedForOtherErrors(t *testing.T) {
	t.Parallel()
	for name, err := range map[string]error{
		"a render error":           errors.New("write conf: disk full"),
		"text only, no sentinel":   errors.New("the broker refused the reload"),
		"unrelated wrapped errors": fmt.Errorf("outer: %w", errors.New("inner")),
	} {
		ring := events.NewRing(8)
		d := &Daemon{events: ring}
		d.emitBusRender("apply", true, err)
		evs := renderEvents(ring)
		if len(evs) != 1 || !strings.Contains(evs[0].Message, "bus config not rendered") {
			t.Errorf("%s: events %+v, want one \"bus config not rendered\"", name, evs)
		}
	}
}

// A refusal with nothing changed cannot occur (no reload runs), but if the
// error is the sentinel and changed is false it is not claimed as rendered.
func TestEmitBusRenderDoesNotClaimARenderWhenNothingChanged(t *testing.T) {
	t.Parallel()
	ring := events.NewRing(8)
	d := &Daemon{events: ring}
	d.emitBusRender("apply", false, fmt.Errorf("x: %w", bus.ErrReloadRefused))
	evs := renderEvents(ring)
	if len(evs) != 1 || !strings.Contains(evs[0].Message, "bus config not rendered") {
		t.Errorf("events %+v, want one \"bus config not rendered\"", evs)
	}
}
