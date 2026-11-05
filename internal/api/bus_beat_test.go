package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

// busBeatSession seeds a running session carrying a token, and a statusline
// reading and heartbeat stamp, so a test can see what a bus beat leaves alone.
func busBeatSession(t *testing.T, s *Store) (key, token string, before Session) {
	t.Helper()
	tok, hash, err := NewHeartbeatToken()
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	sess := &Session{
		Name: "squad-worker-g1-0", Workspace: "ws", Team: "squad", Role: "worker",
		State: SessionRunning, CreatedAt: time.Now().UTC().Add(-time.Hour),
		HeartbeatToken: tok, HeartbeatTokenHash: hash,
	}
	if err := s.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	// A statusline beat, so there is a CTX reading and a LastHeartbeat to protect.
	if _, err := s.UpdateSessionHeartbeat(NewHeartbeatRequestWithToken(sess.Key(), tok, 37, "")); err != nil {
		t.Fatalf("statusline heartbeat: %v", err)
	}
	got, err := s.GetSession(sess.Key())
	if err != nil {
		t.Fatal(err)
	}
	return sess.Key(), tok, got
}

// A bus beat with the session's own token stamps LastBusBeat and nothing else:
// not the CTX reading, not ContextAt, not LastHeartbeat. A beat that moved any
// of them would let the shim vouch for the statusline, or the reverse
// (finding-166).
func TestBusBeatSetsOnlyLastBusBeat(t *testing.T) {
	s := NewStore()
	key, tok, before := busBeatSession(t, s)
	if !before.LastBusBeat.IsZero() {
		t.Fatalf("LastBusBeat = %v before any bus beat, want zero", before.LastBusBeat)
	}
	time.Sleep(2 * time.Millisecond)

	auth, err := s.UpdateSessionBusBeat(BusBeatRequest{SessionKey: key, SessionToken: tok, Source: BusBeatSourceDirectorShim})
	if err != nil {
		t.Fatalf("bus beat with the right token: %v", err)
	}
	if auth != HeartbeatAuthToken {
		t.Errorf("auth = %q, want %q", auth, HeartbeatAuthToken)
	}
	after, err := s.GetSession(key)
	if err != nil {
		t.Fatal(err)
	}
	if after.LastBusBeat.IsZero() {
		t.Error("an admitted bus beat did not stamp LastBusBeat")
	}
	if !after.LastHeartbeat.Equal(before.LastHeartbeat) {
		t.Errorf("LastHeartbeat moved from %v to %v", before.LastHeartbeat, after.LastHeartbeat)
	}
	if !after.ContextAt.Equal(before.ContextAt) || after.ContextPercent != before.ContextPercent ||
		after.ContextSource != before.ContextSource {
		t.Errorf("the CTX reading changed: before %+v, after %+v", before.SessionContext, after.SessionContext)
	}
}

// The token rule is the heartbeat's: no token and a wrong token are refused
// with the same sentinel, and a refused beat stamps nothing.
func TestBusBeatRefusesNoTokenAndWrongToken(t *testing.T) {
	s := NewStore()
	key, _, _ := busBeatSession(t, s)
	_, peerTok, err := NewHeartbeatToken()
	if err != nil {
		t.Fatal(err)
	}
	for name, tok := range map[string]string{"no token": "", "wrong token": peerTok} {
		t.Run(name, func(t *testing.T) {
			_, err := s.UpdateSessionBusBeat(BusBeatRequest{SessionKey: key, SessionToken: tok})
			if !errors.Is(err, ErrHeartbeatUnauthorized) {
				t.Fatalf("err = %v, want ErrHeartbeatUnauthorized", err)
			}
			got, gerr := s.GetSession(key)
			if gerr != nil {
				t.Fatal(gerr)
			}
			if !got.LastBusBeat.IsZero() {
				t.Errorf("a refused bus beat stamped LastBusBeat %v", got.LastBusBeat)
			}
		})
	}
	if _, err := s.UpdateSessionBusBeat(BusBeatRequest{SessionKey: "ws/nobody"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown session: err = %v, want ErrNotFound", err)
	}
}

// A statusline heartbeat leaves LastBusBeat alone, so the two stamps stay two
// producers.
func TestStatuslineHeartbeatLeavesLastBusBeat(t *testing.T) {
	s := NewStore()
	key, tok, _ := busBeatSession(t, s)
	if _, err := s.UpdateSessionBusBeat(BusBeatRequest{SessionKey: key, SessionToken: tok}); err != nil {
		t.Fatal(err)
	}
	mid, _ := s.GetSession(key)
	time.Sleep(2 * time.Millisecond)
	if _, err := s.UpdateSessionHeartbeat(NewHeartbeatRequestWithToken(key, tok, 40, "")); err != nil {
		t.Fatal(err)
	}
	after, _ := s.GetSession(key)
	if !after.LastBusBeat.Equal(mid.LastBusBeat) {
		t.Errorf("a statusline heartbeat moved LastBusBeat from %v to %v", mid.LastBusBeat, after.LastBusBeat)
	}
}

// The request fixture parses into the fields the director shim sends, and
// marshals back to the same bytes. The director repo pins the same file's
// bytes against what its shim builds, so a rename on either side fails a test
// there.
func TestBusBeatRequestFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/bus_beat_request.json")
	if err != nil {
		t.Fatal(err)
	}
	var r BusBeatRequest
	// Strict, so the fixture cannot grow a key the daemon would ignore.
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		t.Fatalf("fixture does not parse: %v", err)
	}
	want := BusBeatRequest{SessionKey: "ws/squad-worker-g1-0", SessionToken: "fixture-token-not-a-secret", Source: BusBeatSourceDirectorShim}
	if r != want {
		t.Fatalf("fixture parsed to %+v, want %+v", r, want)
	}
	back, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != string(raw) {
		t.Errorf("fixture is not byte-equal to the marshalled request:\nfile:  %s\nwire:  %s", raw, back)
	}
}

// LastBusBeat is written through to the file like LastHeartbeat, so a daemon
// restart does not send every bus seat back to the first-beat grace.
func TestBusBeatSurvivesARestart(t *testing.T) {
	path := t.TempDir() + "/marvel.bolt"
	s := NewStore()
	if err := s.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	key, tok, _ := busBeatSession(t, s)
	if _, err := s.UpdateSessionBusBeat(BusBeatRequest{SessionKey: key, SessionToken: tok}); err != nil {
		t.Fatal(err)
	}
	want, _ := s.GetSession(key)
	if err := s.CloseBolt(); err != nil {
		t.Fatal(err)
	}
	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	got, err := s2.GetSession(key)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastBusBeat.IsZero() || !got.LastBusBeat.Equal(want.LastBusBeat) {
		t.Errorf("LastBusBeat after a restart = %v, want %v", got.LastBusBeat, want.LastBusBeat)
	}
}
