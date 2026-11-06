package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// model.mismatch is decided in handleHeartbeat from what the store reports of
// the change (prior observed id, new observed id, declared model). These tests
// build a bare Daemon, so they need no tmux.

func mismatchDaemon(t *testing.T, declared string) (d *Daemon, key, token string) {
	t.Helper()
	d = &Daemon{store: api.NewStore(), events: events.NewRing(events.DefaultCapacity)}
	tok, hash, err := api.NewHeartbeatToken()
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	sess := &api.Session{
		Name: "agent-0", Workspace: "ws", Team: "squad", Role: "worker",
		Runtime:            api.Runtime{Name: "claude", Command: "claude", Model: declared},
		State:              api.SessionRunning,
		CreatedAt:          time.Now().UTC(),
		HeartbeatTokenHash: hash,
		// The mismatch rule is built for a session on the vendor's default
		// backend only; a redirected backend is held (open question 6).
		BackendRedirection: api.BackendDefault,
	}
	if err := d.store.CreateSession(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return d, sess.Key(), tok
}

// beat sends one heartbeat and fails the test on a handler error.
func beat(t *testing.T, d *Daemon, key, token, display, modelID string) {
	t.Helper()
	resp := d.handleHeartbeat(mustMarshal(t, heartbeatParams{
		SessionKey: key, SessionToken: token, ContextPercent: 20,
		Model: display, ModelID: modelID,
	}))
	if resp.Error != "" {
		t.Fatalf("heartbeat: %s", resp.Error)
	}
}

func mismatches(d *Daemon) []events.Event {
	return d.events.Snapshot(events.Filter{Kind: events.KindModelMismatch}, 0)
}

// Test 31. A change of identity emits once; a repeat of the same identity does
// not, a third identity emits again, and a return to the declared identity is
// silent. The repeat row is what the "drop the prior-observed check" mutant
// turns red: comparing only against the declared model would emit on every beat.
func TestModelMismatchEmitsOnChangeNotOnRepeat(t *testing.T) {
	d, key, token := mismatchDaemon(t, "claude-opus-5")

	beat(t, d, key, token, "Sonnet 5.5", "claude-sonnet-5-5")
	got := mismatches(d)
	if len(got) != 1 {
		t.Fatalf("first differing id emitted %d model.mismatch events, want 1", len(got))
	}
	ev := got[0]
	if ev.Severity != events.SeverityWarning {
		t.Errorf("severity = %q, want warning", ev.Severity)
	}
	if ev.Session != key || ev.Workspace != "ws" || ev.Team != "squad" || ev.Role != "worker" {
		t.Errorf("event coordinates = %q %q %q %q, want the session's own", ev.Session, ev.Workspace, ev.Team, ev.Role)
	}
	for _, id := range []string{"claude-opus-5", "claude-sonnet-5-5"} {
		if !strings.Contains(ev.Message, id) {
			t.Errorf("message %q does not carry %q", ev.Message, id)
		}
	}

	beat(t, d, key, token, "Sonnet 5.5", "claude-sonnet-5-5")
	beat(t, d, key, token, "Sonnet 5.5", "claude-sonnet-5-5")
	if n := len(mismatches(d)); n != 1 {
		t.Fatalf("a repeat beat on the same identity left %d events, want 1", n)
	}

	beat(t, d, key, token, "Haiku 4.5", "claude-haiku-4-5")
	if n := len(mismatches(d)); n != 2 {
		t.Fatalf("a third identity left %d events, want 2", n)
	}

	beat(t, d, key, token, "Opus 5", "claude-opus-5")
	if n := len(mismatches(d)); n != 2 {
		t.Fatalf("a change back to the declared identity left %d events, want 2", n)
	}
}

// Test 32. The comparison reads ModelID only. A display name that differs from
// the declared model, with no id, says nothing; a display name that matches
// the declared model, with a different id, still emits.
func TestModelMismatchReadsModelIDNotDisplayName(t *testing.T) {
	d, key, token := mismatchDaemon(t, "claude-opus-5")

	beat(t, d, key, token, "Sonnet 5.5", "")
	if n := len(mismatches(d)); n != 0 {
		t.Fatalf("a differing display name with an empty ModelID emitted %d events, want 0", n)
	}
	beat(t, d, key, token, "Sonnet 5.5", "claude-opus-5")
	if n := len(mismatches(d)); n != 0 {
		t.Fatalf("a differing display name over the declared id emitted %d events, want 0", n)
	}
	beat(t, d, key, token, "claude-opus-5", "claude-sonnet-5-5")
	if n := len(mismatches(d)); n != 1 {
		t.Fatalf("a matching display name over a different id emitted %d events, want 1", n)
	}
}

// A respelling of the same model is not a change of model: a Bedrock prefix, a
// dated suffix and [1m] all share an identity key with the declared id.
func TestModelMismatchSilentOnRespelledSameModel(t *testing.T) {
	d, key, token := mismatchDaemon(t, "claude-opus-5")

	beat(t, d, key, token, "Opus 5", "us.anthropic.claude-opus-5-20260101")
	beat(t, d, key, token, "Opus 5", "claude-opus-5")
	beat(t, d, key, token, "Opus 5 (1M context)", "claude-opus-5[1m]")
	if n := len(mismatches(d)); n != 0 {
		t.Fatalf("respellings of the declared model emitted %d events, want 0", n)
	}
}

// Nothing is declared to differ from when the declared model is empty or is an
// alias (opus, sonnet), so neither emits however the harness answers.
func TestModelMismatchSilentWithoutAFullDeclaredID(t *testing.T) {
	for _, declared := range []string{"", "opus", "sonnet"} {
		d, key, token := mismatchDaemon(t, declared)
		beat(t, d, key, token, "Haiku 4.5", "claude-haiku-4-5")
		if n := len(mismatches(d)); n != 0 {
			t.Errorf("declared %q emitted %d events, want 0", declared, n)
		}
	}
}
