package daemon

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// ringSeat is a running session whose pane does not exist, so a ring reaches
// the claim without a tmux seat behind it. A send to it fails, which is the
// failed-send case.
func ringSeat(t *testing.T, d *Daemon) api.Session {
	t.Helper()
	sess := &api.Session{
		Name: "worker-0", Workspace: "ringws", Team: "squad", Role: "worker",
		State: api.SessionRunning, PaneID: "%999999",
		Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
	}
	if err := d.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	got, err := d.store.GetSession(sess.Key())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func ringParams(t *testing.T, key, text string, from Injector) []byte {
	t.Helper()
	return mustMarshal(t, map[string]any{
		"session_key": key, "text": text, "literal": true, "enter": true,
		"injector": from, "ring": true,
	})
}

func claimOf(t *testing.T, d *Daemon, key string) *api.RingClaim {
	t.Helper()
	got, err := d.store.GetSession(key)
	if err != nil {
		t.Fatal(err)
	}
	return got.RingClaim
}

// The ringer is what the daemon already records for an inject: the transport it
// saw and what the caller declared, without the flag or override suffixes.
func TestRingClaimNamesTheRingerLikeTheInjectEvent(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := ringSeat(t, d)
	now := time.Date(2026, 10, 25, 4, 0, 0, 0, time.UTC)
	if err := d.ringClaim(sess, localCaller(), Injector{Session: "seat-x", User: "example-user"}, now); err != nil {
		t.Fatalf("ringClaim: %v", err)
	}
	c := claimOf(t, d, sess.Key())
	want := "transport=local declared_session=seat-x declared_user=example-user"
	if c == nil || c.Ringer != want || c.Result != api.RingPending || !c.RingAt.Equal(now) {
		t.Fatalf("claim = %+v, want pending at %v by %q", c, now, want)
	}
	if strings.Contains(c.Ringer, "flag=") || strings.Contains(c.Ringer, "override=") {
		t.Errorf("ringer %q carries an inject suffix", c.Ringer)
	}
}

func TestRingSettleMarksSentOrFailed(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := ringSeat(t, d)
	at := time.Date(2026, 10, 25, 4, 0, 0, 0, time.UTC)
	if err := d.ringClaim(sess, localCaller(), Injector{}, at); err != nil {
		t.Fatal(err)
	}
	d.ringSettle(sess, at, nil)
	if c := claimOf(t, d, sess.Key()); c == nil || c.Result != api.RingSent {
		t.Fatalf("after a send: claim = %+v, want sent", c)
	}
	later := at.Add(api.RingClaimWindow)
	if err := d.ringClaim(sess, localCaller(), Injector{}, later); err != nil {
		t.Fatal(err)
	}
	d.ringSettle(sess, later, errors.New("no such pane"))
	if c := claimOf(t, d, sess.Key()); c == nil || c.Result != api.RingFailed {
		t.Fatalf("after a failed send: claim = %+v, want failed", c)
	}
}

// A ring inside the window is refused with the holder named, the refusal is
// recorded like the other inject refusals, and the claim is not touched.
func TestRingInjectInsideTheWindowIsRefused(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := ringSeat(t, d)
	held := time.Now().UTC().Add(-10 * time.Second)
	if err := d.store.ClaimRing(sess.Key(), "the-holder", held); err != nil {
		t.Fatal(err)
	}
	if err := d.store.SettleRing(sess.Key(), held, api.RingSent); err != nil {
		t.Fatal(err)
	}

	resp := d.handleInjectAs(ringParams(t, sess.Key(), "wake", Injector{Session: "other-seat"}), localCaller())
	for _, want := range []string{"ring refused", "the-holder", "window 120s"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error %q lacks %q", resp.Error, want)
		}
	}
	if c := claimOf(t, d, sess.Key()); c == nil || c.Ringer != "the-holder" || c.Result != api.RingSent || !c.RingAt.Equal(held) {
		t.Fatalf("a refused ring changed the claim: %+v", c)
	}
	got := d.events.Snapshot(events.Filter{Kind: events.KindSessionInjectRefused, Session: sess.Name}, 10)
	if len(got) != 1 || !strings.Contains(got[0].Message, "ring refused") {
		t.Fatalf("inject-refused events = %+v, want one naming the ring refusal", got)
	}
	if n := len(d.events.Snapshot(events.Filter{Kind: events.KindSessionInjected, Session: sess.Name}, 10)); n != 0 {
		t.Errorf("a refused ring was recorded as sent (%d events)", n)
	}
}

// An inject without --ring behaves as before: a held claim neither refuses it
// nor changes.
func TestPlainInjectIgnoresTheRingClaim(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := ringSeat(t, d)
	held := time.Now().UTC()
	if err := d.store.ClaimRing(sess.Key(), "the-holder", held); err != nil {
		t.Fatal(err)
	}
	params := mustMarshal(t, map[string]any{"session_key": sess.Key(), "text": "x", "literal": true, "injector": Injector{}})
	resp := d.handleInjectAs(params, localCaller())
	if strings.Contains(resp.Error, "ring refused") {
		t.Fatalf("a plain inject was refused by the ring claim: %q", resp.Error)
	}
	if c := claimOf(t, d, sess.Key()); c == nil || c.Ringer != "the-holder" || c.Result != api.RingPending {
		t.Fatalf("a plain inject changed the claim: %+v", c)
	}
}

// The pane is gone, so the send fails: the claim reads failed and does not
// hold the next ring.
func TestRingInjectThatFailsToSendLeavesAFailedClaim(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := ringSeat(t, d)
	resp := d.handleInjectAs(ringParams(t, sess.Key(), "wake", Injector{}), localCaller())
	if resp.Error == "" || strings.Contains(resp.Error, "ring refused") {
		t.Fatalf("first ring error = %q, want a send failure", resp.Error)
	}
	if c := claimOf(t, d, sess.Key()); c == nil || c.Result != api.RingFailed {
		t.Fatalf("claim after a failed send = %+v, want failed", c)
	}
	resp = d.handleInjectAs(ringParams(t, sess.Key(), "wake", Injector{}), localCaller())
	if strings.Contains(resp.Error, "ring refused") {
		t.Fatalf("a ring after a failed one was refused: %q", resp.Error)
	}
}

// Q7: a refused inject leaves no claim. The ring is set after the clear,
// preflight and bare-digit refusals, so an inject they refuse must not hold the
// seat for a window with no signal.
func TestRefusedRingInjectLeavesNoClaim(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runtime string
		extra   map[string]any
		want    string
	}{
		// No safe clear key is known for this runtime, so --clear is refused
		// before anything is read or typed.
		{"clear refused", "sleep", map[string]any{"clear": true}, "refusing --clear"},
		// claude is read before it is typed into; the pane cannot be read, so
		// the preflight refuses.
		{"preflight refused", "claude", nil, "not typed into blind"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := bareDigitDaemon(t)
			sess := ringSeat(t, d)
			if err := d.store.UpdateSession(sess.Key(), func(s *api.Session) error {
				s.Runtime.Name = tc.runtime
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			p := map[string]any{"session_key": sess.Key(), "text": "wake", "literal": true, "enter": true, "injector": Injector{}, "ring": true}
			for k, v := range tc.extra {
				p[k] = v
			}
			resp := d.handleInjectAs(mustMarshal(t, p), localCaller())
			if !strings.Contains(resp.Error, tc.want) {
				t.Fatalf("error = %q, want the refusal containing %q", resp.Error, tc.want)
			}
			if c := claimOf(t, d, sess.Key()); c != nil {
				t.Fatalf("a refused ring inject left a claim: %+v", c)
			}
		})
	}
}

// After a refused ring inject, a ring with nothing refusing it is not held back
// by the refusal: it reaches the send (this pane is gone, so the send fails and
// the claim reads failed).
func TestRingInjectAfterARefusedOneIsNotHeld(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := ringSeat(t, d)
	refused := map[string]any{"session_key": sess.Key(), "text": "wake", "literal": true, "enter": true, "injector": Injector{}, "ring": true, "clear": true}
	if resp := d.handleInjectAs(mustMarshal(t, refused), localCaller()); !strings.Contains(resp.Error, "refusing --clear") {
		t.Fatalf("first ring error = %q, want the clear refusal", resp.Error)
	}
	resp := d.handleInjectAs(ringParams(t, sess.Key(), "wake", Injector{}), localCaller())
	if resp.Error == "" || strings.Contains(resp.Error, "ring refused") || strings.Contains(resp.Error, "refusing --clear") {
		t.Fatalf("second ring error = %q, want a send failure, not a ring or clear refusal", resp.Error)
	}
	if c := claimOf(t, d, sess.Key()); c == nil || c.Result != api.RingFailed {
		t.Fatalf("claim after the second ring = %+v, want failed (it reached the send)", c)
	}
}

// The bare-digit refusal sits just before the ring too. This one needs a real
// pane, so it runs where tmux runs seats; a refused bare digit leaves no claim,
// and a ring that nothing refuses then goes through and reads sent.
func TestRefusedBareDigitRingInjectLeavesNoClaim(t *testing.T) {
	d := bareDigitDaemon(t)
	key := verifySeat(t, d, quietSeat, "claude", "composer ready")
	resp := injectOf(t, d, map[string]any{"session_key": key, "text": "3", "literal": true, "enter": true, "ring": true})
	if !strings.Contains(resp.Error, "cannot rule out") {
		t.Fatalf("error = %q, want the bare-digit refusal", resp.Error)
	}
	if c := claimOf(t, d, key); c != nil {
		t.Fatalf("a refused bare digit ring left a claim: %+v", c)
	}
	if resp := injectOf(t, d, map[string]any{"session_key": key, "text": "hello", "literal": true, "enter": true, "ring": true}); resp.Error != "" {
		t.Fatalf("a ring that nothing refuses failed: %s", resp.Error)
	}
	if c := claimOf(t, d, key); c == nil || c.Result != api.RingSent {
		t.Fatalf("claim after the second ring = %+v, want sent", c)
	}
}
