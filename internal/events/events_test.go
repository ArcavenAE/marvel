package events

import (
	"sync"
	"testing"
	"time"
)

func TestRingAppendsUntilFull(t *testing.T) {
	r := NewRing(3)
	for i := 0; i < 3; i++ {
		r.Emit(Event{Kind: KindSessionCreated, Session: "s", Message: string(rune('a' + i))})
	}
	if got := r.Len(); got != 3 {
		t.Fatalf("Len=%d, want 3", got)
	}
	snap := r.Snapshot(Filter{}, 0)
	if len(snap) != 3 {
		t.Fatalf("Snapshot len=%d, want 3", len(snap))
	}
	if snap[0].Message != "a" || snap[2].Message != "c" {
		t.Fatalf("expected a..c, got %q..%q", snap[0].Message, snap[2].Message)
	}
}

func TestRingOverwritesOldestWhenFull(t *testing.T) {
	r := NewRing(3)
	for i := 0; i < 5; i++ {
		r.Emit(Event{Kind: KindSessionCreated, Message: string(rune('a' + i))})
	}
	if got := r.Len(); got != 3 {
		t.Fatalf("Len=%d, want 3 (capacity)", got)
	}
	snap := r.Snapshot(Filter{}, 0)
	// After 5 emits into capacity-3, survivors are c, d, e (oldest first).
	if len(snap) != 3 {
		t.Fatalf("Snapshot len=%d, want 3", len(snap))
	}
	if snap[0].Message != "c" || snap[1].Message != "d" || snap[2].Message != "e" {
		t.Fatalf("expected c,d,e — got %q,%q,%q", snap[0].Message, snap[1].Message, snap[2].Message)
	}
}

func TestRingSnapshotLimitN(t *testing.T) {
	r := NewRing(10)
	for i := 0; i < 10; i++ {
		r.Emit(Event{Kind: KindSessionCreated, Message: string(rune('a' + i))})
	}
	snap := r.Snapshot(Filter{}, 3)
	if len(snap) != 3 {
		t.Fatalf("Snapshot(n=3) len=%d, want 3", len(snap))
	}
	// Tail should be the newest 3: h, i, j.
	if snap[0].Message != "h" || snap[2].Message != "j" {
		t.Fatalf("expected h..j, got %q..%q", snap[0].Message, snap[2].Message)
	}
}

func TestRingFilterBySession(t *testing.T) {
	r := NewRing(10)
	r.Emit(Event{Kind: KindSessionCreated, Session: "foo/a", Message: "hit"})
	r.Emit(Event{Kind: KindSessionCreated, Session: "foo/b", Message: "miss"})
	r.Emit(Event{Kind: KindSessionCrashed, Session: "foo/a", Message: "hit"})

	snap := r.Snapshot(Filter{Session: "foo/a"}, 0)
	if len(snap) != 2 {
		t.Fatalf("expected 2 events for foo/a, got %d", len(snap))
	}
	for _, ev := range snap {
		if ev.Message != "hit" {
			t.Fatalf("wrong event in filtered snapshot: %+v", ev)
		}
	}
}

func TestRingFilterBySeverity(t *testing.T) {
	r := NewRing(10)
	r.Emit(Event{Kind: KindSessionCreated, Severity: SeverityInfo, Message: "info"})
	r.Emit(Event{Kind: KindSessionCrashed, Severity: SeverityWarning, Message: "warn"})

	snap := r.Snapshot(Filter{MinSeverity: SeverityWarning}, 0)
	if len(snap) != 1 {
		t.Fatalf("expected 1 warning event, got %d", len(snap))
	}
	if snap[0].Message != "warn" {
		t.Fatalf("filtered event wrong: %+v", snap[0])
	}
}

func TestEmitNilEmitterNoPanic(t *testing.T) {
	// The zero-emission sugar has to be nil-safe — producers in
	// session.Manager / team.Controller receive the ring through
	// dependency injection and may legitimately have no emitter.
	Emit(nil, Event{Kind: KindSessionCreated})
}

func TestEmitStampsAndSeverityDefault(t *testing.T) {
	r := NewRing(1)
	Emit(r, Event{Kind: KindSessionCreated})
	snap := r.Snapshot(Filter{}, 0)
	if len(snap) != 1 {
		t.Fatalf("expected 1 event, got %d", len(snap))
	}
	if snap[0].Timestamp.IsZero() {
		t.Error("expected Emit to stamp Timestamp")
	}
	if snap[0].Severity != SeverityInfo {
		t.Errorf("expected default Severity=info, got %q", snap[0].Severity)
	}
}

func TestRingConcurrentEmit(t *testing.T) {
	// Smoke test — must not deadlock or race (the -race build catches
	// the latter). Goal is just concurrent access, not a specific count.
	r := NewRing(1000)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				r.Emit(Event{Kind: KindSessionCreated, Timestamp: time.Now(), Message: "x"})
			}
		}()
	}
	wg.Wait()
	if r.Len() == 0 {
		t.Fatal("expected some events after concurrent emit")
	}
}
