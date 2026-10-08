package daemon

import (
	"encoding/json"
	"path/filepath"
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

// describe session names the delivery next to the sources, says so for a
// record written before the delivery was kept, and says nothing where marvel
// passed no sources. The note is a read-time view and is never stored
// (marvel#748).
func TestDescribeNamesTheSettingSourcesDelivery(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := liveSession(t, d)
	describe := func(sources, delivery string) (string, api.Session) {
		t.Helper()
		if err := d.store.UpdateSession(sess.Key(), func(live *api.Session) error {
			live.SettingSources, live.SettingSourcesDelivery = sources, delivery
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		resp := d.handleDescribe(mustMarshal(t, map[string]string{"resource_type": "session", "name": sess.Key()}))
		if resp.Error != "" {
			t.Fatalf("describe: %s", resp.Error)
		}
		stored, err := d.store.GetSession(sess.Key())
		if err != nil {
			t.Fatal(err)
		}
		return string(resp.Result), stored
	}

	out, _ := describe("project,local", api.SettingSourcesShellText)
	for _, want := range []string{`"SettingSources":"project,local"`, `"SettingSourcesDelivery":"shell-text"`} {
		if !strings.Contains(out, want) {
			t.Errorf("describe lacks %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "delivery not recorded") {
		t.Errorf("a recorded delivery drew the legacy note:\n%s", out)
	}

	out, stored := describe("user,project,local", "")
	if !strings.Contains(out, `"setting_sources_note":"delivery not recorded"`) {
		t.Errorf("a legacy record lacks the note:\n%s", out)
	}
	if stored.SettingSourcesNote != "" || stored.SettingSourcesDelivery != "" {
		t.Errorf("the legacy view was stored: note %q delivery %q", stored.SettingSourcesNote, stored.SettingSourcesDelivery)
	}

	out, _ = describe("", "")
	if strings.Contains(out, "delivery not recorded") || strings.Contains(out, "setting_sources_note") {
		t.Errorf("a session with no sources drew a note:\n%s", out)
	}
}

// The describe note is a read-time view: describing a legacy record leaves no
// note in what bolt holds, across a restart (marvel#748).
func TestDescribeNoteIsNeverPersisted(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := liveSession(t, d)
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	if err := d.store.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt: %v", err)
	}
	if err := d.store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.SettingSources, live.SettingSourcesDelivery = "user,project,local", ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	resp := d.handleDescribe(mustMarshal(t, map[string]string{"resource_type": "session", "name": sess.Key()}))
	if resp.Error != "" || !strings.Contains(string(resp.Result), "delivery not recorded") {
		t.Fatalf("describe did not draw the note: %q %s", resp.Error, resp.Result)
	}
	if err := d.store.CloseBolt(); err != nil {
		t.Fatal(err)
	}
	reopened := api.NewStore()
	if err := reopened.OpenBolt(path); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.CloseBolt() })
	got, err := reopened.GetSession(sess.Key())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if got.SettingSources != "user,project,local" {
		t.Fatalf("test premise: the sources did not persist: %q", got.SettingSources)
	}
	if strings.Contains(string(raw), "setting_sources_note") || got.SettingSourcesNote != "" {
		t.Errorf("the note reached bolt: %s", raw)
	}
}
