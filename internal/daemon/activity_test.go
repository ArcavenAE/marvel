package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// The daemon fills the tick-ring reading on the copies it serves, from the
// controller, and never on the stored session: nothing about it persists.
func TestGetSessionsStampsTheActivityReadingOnCopies(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := liveSession(t, d)
	d.teamCtrl.SetClusterQuietWindow(7 * time.Minute)

	resp := d.handleGet(mustMarshal(t, map[string]string{"resource_type": "sessions"}))
	if resp.Error != "" {
		t.Fatalf("get: %s", resp.Error)
	}
	var got []api.Session
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatal(err)
	}
	var served *api.Session
	for i := range got {
		if got[i].Key() == sess.Key() {
			served = &got[i]
		}
	}
	if served == nil {
		t.Fatal("the session is not in the listing")
	}
	if served.ActiveTicks.Window != 7*time.Minute {
		t.Errorf("served window = %v, want the controller's 7m", served.ActiveTicks.Window)
	}
	stored, err := d.store.GetSession(sess.Key())
	if err != nil {
		t.Fatal(err)
	}
	if stored.ActiveTicks != (api.ActiveTicks{}) {
		t.Errorf("the stored session carries the reading: %+v", stored.ActiveTicks)
	}
}

// describe session shows the window and the source side by side, so an operator
// can tell what a cell's number meant.
func TestDescribeNamesActivitySource(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := liveSession(t, d)
	if err := d.store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.ContextSource = api.ContextSourceHeartbeat
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	resp := d.handleDescribe(mustMarshal(t, map[string]string{"resource_type": "session", "name": sess.Key()}))
	if resp.Error != "" {
		t.Fatalf("describe: %s", resp.Error)
	}
	out := string(resp.Result)
	for _, want := range []string{`"active_ticks"`, `"window"`, `"ContextSource":"heartbeat"`} {
		if !strings.Contains(out, want) {
			t.Errorf("describe session lacks %s:\n%s", want, out)
		}
	}
}
