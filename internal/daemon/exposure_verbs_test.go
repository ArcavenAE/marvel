package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/events"
)

// The exposure verbs, their authority, the refusal at apply and the describe
// line (marvel#945, PR 2). Each drives the real dispatch.

const exposureVerbsManifest = `
workspace:
  name: xverb
teams:
  - name: crew
    roles:
      - name: writer
        replicas: 1
        writes: [parse]
        runtime:
          command: sleep
          args: ["300"]
`

func exposureVerbsDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := newHandlerDaemon(t)
	if err := d.store.CreateWorkspace(&api.Workspace{Name: "xverb"}); err != nil {
		t.Fatal(err)
	}
	if err := d.store.CreateTeam(&api.Team{Name: "crew", Workspace: "xverb", Roles: []api.Role{
		{Name: "writer", Replicas: 0, Writes: []string{"parse"}},
	}}); err != nil {
		t.Fatal(err)
	}
	return d
}

func exposureParams(t *testing.T, extra map[string]any) json.RawMessage {
	t.Helper()
	m := map[string]any{"workspace": "xverb", "team": "crew", "role": "writer", "function": "parse"}
	for k, v := range extra {
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func eventsOfKind(d *Daemon, k events.Kind) []events.Event {
	return d.events.Snapshot(events.Filter{Kind: k}, 0)
}

// TestExposureAddEndVoidThroughDispatch: the three verbs write the store and say
// so on the event ring, the void loudly and with its reason.
func TestExposureAddEndVoidThroughDispatch(t *testing.T) {
	d := exposureVerbsDaemon(t)

	resp := d.dispatchAs(daemonrpc.Request{Method: "exposure.add", Params: exposureParams(t, map[string]any{
		"paths": []string{"src/parse"}, "caster": "supervisor",
	})}, localCaller())
	if resp.Error != "" {
		t.Fatalf("exposure.add: %s", resp.Error)
	}
	recs, _ := d.store.ExposuresForSlot("xverb", "crew", "writer")
	if len(recs) != 1 || recs[0].Caster != "supervisor" || recs[0].Paths[0] != "src/parse" {
		t.Fatalf("records after add = %+v, want the one recorded", recs)
	}
	if n := len(eventsOfKind(d, events.KindExposureRecorded)); n != 1 {
		t.Errorf("exposure.recorded events = %d, want 1", n)
	}

	for _, bad := range []string{"par se", "pa\u200brse", ""} {
		if r := d.dispatchAs(daemonrpc.Request{Method: "exposure.add", Params: exposureParams(t, map[string]any{"function": bad})}, localCaller()); r.Error == "" {
			t.Errorf("exposure.add accepted the name %q", bad)
		}
	}

	if r := d.dispatchAs(daemonrpc.Request{Method: "exposure.end", Params: exposureParams(t, nil)}, localCaller()); r.Error != "" {
		t.Fatalf("exposure.end: %s", r.Error)
	}
	if recs, _ = d.store.ExposuresForSlot("xverb", "crew", "writer"); recs[0].EndedAt.IsZero() {
		t.Error("exposure.end left the record open")
	}
	if n := len(eventsOfKind(d, events.KindExposureEnded)); n != 1 {
		t.Errorf("exposure.ended events = %d, want 1", n)
	}

	if r := d.dispatchAs(daemonrpc.Request{Method: "exposure.void", Params: exposureParams(t, nil)}, localCaller()); r.Error == "" {
		t.Error("exposure.void with no reason was accepted")
	}
	r := d.dispatchAs(daemonrpc.Request{Method: "exposure.void", Params: exposureParams(t, map[string]any{"reason": "wrong role"})}, localCaller())
	if r.Error != "" {
		t.Fatalf("exposure.void: %s", r.Error)
	}
	if recs, _ = d.store.ExposuresForSlot("xverb", "crew", "writer"); len(recs) != 1 || recs[0].VoidedAt.IsZero() || recs[0].VoidReason != "wrong role" {
		t.Errorf("records after void = %+v, want the one record, voided, with its reason", recs)
	}
	voided := eventsOfKind(d, events.KindExposureVoided)
	if len(voided) != 1 || voided[0].Severity != events.SeverityWarning || !strings.Contains(voided[0].Message, "wrong role") {
		t.Errorf("exposure.voided events = %+v, want one warning carrying the reason", voided)
	}
}

// TestExposureVoidIsLocalSocketOnlyAndCredentialKeysCannotRecord: a void is only
// served on the local socket, and a credential-push key reaches none of them.
func TestExposureVoidIsLocalSocketOnlyAndCredentialKeysCannotRecord(t *testing.T) {
	d := exposureVerbsDaemon(t)
	if r := d.dispatchAs(daemonrpc.Request{Method: "exposure.add", Params: exposureParams(t, nil)}, localCaller()); r.Error != "" {
		t.Fatalf("setup: %s", r.Error)
	}
	tunnelled := caller{scope: ScopeAdmin, fingerprint: "SHA256:abc"}
	r := d.dispatchAs(daemonrpc.Request{Method: "exposure.void", Params: exposureParams(t, map[string]any{"reason": "x"})}, tunnelled)
	if !strings.Contains(r.Error, "local unix socket") {
		t.Errorf("a tunnelled void: error = %q, want the local-socket refusal", r.Error)
	}
	if recs, _ := d.store.ExposuresForSlot("xverb", "crew", "writer"); recs[0].VoidedAt.After(recs[0].StartedAt) {
		t.Error("a refused void changed the record")
	}
	push := caller{scope: ScopeCredentialPush, fingerprint: "SHA256:def"}
	for _, m := range []string{"exposure.add", "exposure.end", "exposure.void"} {
		r := d.dispatchAs(daemonrpc.Request{Method: m, Params: exposureParams(t, map[string]any{"reason": "x"})}, push)
		if !strings.Contains(r.Error, "not permitted") {
			t.Errorf("%s for a credential-push key: error = %q, want a scope refusal", m, r.Error)
		}
	}
}

// TestApplyRefusesAManifestThatCastsAnExposedWriter: marvel work cannot cast a
// writer whose slot has read its function; nothing is stored.
func TestApplyRefusesAManifestThatCastsAnExposedWriter(t *testing.T) {
	d := newHandlerDaemon(t)
	if _, err := d.store.RecordExposure(api.ExposureRecord{
		Workspace: "xverb", Team: "crew", Role: "writer", Function: "parse", Caster: "supervisor",
	}); err != nil {
		t.Fatal(err)
	}
	resp := applyManifest(t, d, exposureVerbsManifest)
	if !strings.Contains(resp.Error, "exposure") || !strings.Contains(resp.Error, "xverb/crew") {
		t.Fatalf("apply error = %q, want a refusal naming the slot and the exposure", resp.Error)
	}
	if _, err := d.store.GetTeam("xverb/crew"); err == nil {
		t.Error("a refused apply stored the team")
	}
	if rows := d.store.ListSessionsByTeam("xverb", "crew"); len(rows) != 0 {
		t.Errorf("a refused apply left %d session rows", len(rows))
	}
}

// TestDescribeTeamNamesExposure: the held writer and a voided record each get a
// line in describe, so an operator reading the team sees why a role is not
// starting and what was voided.
func TestDescribeTeamNamesExposure(t *testing.T) {
	d := exposureVerbsDaemon(t)
	if r := d.dispatchAs(daemonrpc.Request{Method: "exposure.add", Params: exposureParams(t, nil)}, localCaller()); r.Error != "" {
		t.Fatalf("setup: %s", r.Error)
	}
	desc, err := d.describeTeam("xverb/crew")
	if err != nil {
		t.Fatal(err)
	}
	if !hasCondition(desc, "ExposureHeld", "writer", "parse") {
		t.Errorf("conditions = %+v, want ExposureHeld for writer naming parse", desc.Conditions)
	}
	if r := d.dispatchAs(daemonrpc.Request{Method: "exposure.void", Params: exposureParams(t, map[string]any{"reason": "wrong role"})}, localCaller()); r.Error != "" {
		t.Fatalf("void: %s", r.Error)
	}
	desc, _ = d.describeTeam("xverb/crew")
	if hasCondition(desc, "ExposureHeld", "writer", "") {
		t.Errorf("conditions = %+v, want no ExposureHeld after the void", desc.Conditions)
	}
	if !hasCondition(desc, "ExposureVoided", "writer", "wrong role") {
		t.Errorf("conditions = %+v, want ExposureVoided naming the reason", desc.Conditions)
	}
}

func hasCondition(d teamDescription, typ, role, contains string) bool {
	for _, c := range d.Conditions {
		if c.Type == typ && c.Role == role && strings.Contains(c.Message, contains) {
			return true
		}
	}
	return false
}
