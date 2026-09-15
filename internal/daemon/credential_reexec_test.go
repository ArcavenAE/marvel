package daemon

import (
	"strconv"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/events"
)

// A daemon that goes away (detach, reexec, teardown) holding transient
// credentials names the loss in the departing process, since the successor
// starts empty and cannot know what was dropped (brief 9 S4).
func TestDetachWarnsTransientCredentialsDropped(t *testing.T) {
	d := newHandlerDaemon(t)
	for _, name := range []string{"bus/leaf", "bus/hub"} {
		if resp := d.dispatchAs(Request{Method: "credential.put", Params: putParams(t, name, "DROPSEED-"+name)}, localCaller()); resp.Error != "" {
			t.Fatalf("put %s: %s", name, resp.Error)
		}
	}

	d.Detach()

	evs := d.events.Snapshot(events.Filter{Kind: events.KindCredentialTransientDropped}, 0)
	if len(evs) != 1 {
		t.Fatalf("transient-dropped events = %d, want 1", len(evs))
	}
	ev := evs[0]
	if ev.Severity != events.SeverityWarning {
		t.Errorf("severity = %q, want warning", ev.Severity)
	}
	if !strings.Contains(ev.Message, strconv.Itoa(2)) {
		t.Errorf("message %q does not carry the dropped count 2", ev.Message)
	}
	if !strings.Contains(ev.Message, "push them again") {
		t.Errorf("message %q does not tell the operator to push again", ev.Message)
	}
	if strings.Contains(ev.Message, "DROPSEED") {
		t.Errorf("message leaked a credential value: %q", ev.Message)
	}
}

// A daemon holding no credentials says nothing: the warning is for a real loss,
// not every restart.
func TestDetachSilentWithoutTransientCredentials(t *testing.T) {
	d := newHandlerDaemon(t)
	d.Detach()
	if evs := d.events.Snapshot(events.Filter{Kind: events.KindCredentialTransientDropped}, 0); len(evs) != 0 {
		t.Errorf("transient-dropped events = %d on a daemon with no credentials, want 0", len(evs))
	}
}
