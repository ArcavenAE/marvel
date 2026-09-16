package events

import (
	"sync"
	"testing"
	"time"
)

// recv reads one event from w with a bound, so a delivery bug fails the
// test instead of hanging it.
func recv(t *testing.T, w *Watch) Event {
	t.Helper()
	select {
	case ev, ok := <-w.Events():
		if !ok {
			t.Fatal("watch channel closed")
		}
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("no event delivered within 2s")
	}
	return Event{}
}

func TestWatchDeliversEventsEmittedAfterCall(t *testing.T) {
	r := NewRing(10)
	r.Emit(Event{Kind: KindSessionCreated, Message: "before"})
	w := r.Watch(Filter{}, 0)
	defer w.Close()
	r.Emit(Event{Kind: KindSessionCreated, Message: "after"})

	got := recv(t, w)
	if got.Message != "after" || got.Seq != 2 {
		t.Fatalf("got %+v, want the post-Watch event with Seq 2", got)
	}
	select {
	case ev := <-w.Events():
		t.Fatalf("unexpected second delivery: %+v", ev)
	default:
	}
}

func TestWatchFilterScopesDelivery(t *testing.T) {
	r := NewRing(10)
	w := r.Watch(Filter{Kind: KindSessionCrashed}, 0)
	defer w.Close()
	r.Emit(Event{Kind: KindSessionCreated, Message: "no"})
	r.Emit(Event{Kind: KindSessionCrashed, Message: "yes"})
	r.Emit(Event{Kind: KindSessionDeleted, Message: "no"})

	got := recv(t, w)
	if got.Kind != KindSessionCrashed {
		t.Fatalf("filter leaked: got kind %s", got.Kind)
	}
	select {
	case ev := <-w.Events():
		t.Fatalf("filter leaked a second event: %+v", ev)
	default:
	}
	// A filtered-out event is not a drop: Gap stays zero.
	if d, _ := w.Gap(); d != 0 {
		t.Fatalf("Gap dropped=%d after filtered emits, want 0", d)
	}
}

func TestWatchOverflowDropsOldestAndReportsGap(t *testing.T) {
	r := NewRing(100)
	w := r.Watch(Filter{}, 3)
	defer w.Close()
	for i := 1; i <= 5; i++ {
		r.Emit(Event{Kind: KindSessionCreated, Message: "e"})
	}
	// Seq 1 and 2 were dropped to make room for 4 and 5.
	dropped, resume := w.Gap()
	if dropped != 2 {
		t.Fatalf("dropped=%d, want 2", dropped)
	}
	if resume != 0 {
		// firstDropped is Seq 1, so the resume cursor is 0: Snapshot
		// with SinceSeq 0 returns everything, which includes Seq 1.
		t.Fatalf("resumeFrom=%d, want 0", resume)
	}
	for want := uint64(3); want <= 5; want++ {
		if got := recv(t, w); got.Seq != want {
			t.Fatalf("after overflow got Seq %d, want %d", got.Seq, want)
		}
	}
	// Gap resets on read.
	if d, rf := w.Gap(); d != 0 || rf != 0 {
		t.Fatalf("second Gap=(%d,%d), want (0,0)", d, rf)
	}
}

func TestWatchGapResumeCursorBackfillsFromSnapshot(t *testing.T) {
	r := NewRing(100)
	for i := 0; i < 10; i++ {
		r.Emit(Event{Kind: KindSessionCreated})
	}
	w := r.Watch(Filter{}, 2)
	defer w.Close()
	for i := 0; i < 5; i++ { // Seq 11..15; 11,12,13 dropped
		r.Emit(Event{Kind: KindSessionCreated})
	}
	dropped, resume := w.Gap()
	if dropped != 3 || resume != 10 {
		t.Fatalf("Gap=(%d,%d), want (3,10)", dropped, resume)
	}
	back := r.Snapshot(Filter{SinceSeq: resume}, 0)
	if len(back) != 5 || back[0].Seq != 11 {
		t.Fatalf("backfill from %d returned %d events starting at %d, want 5 from 11", resume, len(back), back[0].Seq)
	}
}

func TestWatchCloseIsIdempotentAndStopsDelivery(t *testing.T) {
	r := NewRing(10)
	w := r.Watch(Filter{}, 0)
	w.Close()
	w.Close()
	// An Emit after Close must not panic on the closed channel.
	r.Emit(Event{Kind: KindSessionCreated})
	if _, ok := <-w.Events(); ok {
		t.Fatal("expected closed channel after Close")
	}
	r.mu.Lock()
	n := len(r.watches)
	r.mu.Unlock()
	if n != 0 {
		t.Fatalf("watch set has %d entries after Close, want 0", n)
	}
}

func TestWatchCloseRemovesOnlyItself(t *testing.T) {
	r := NewRing(10)
	a := r.Watch(Filter{}, 0)
	b := r.Watch(Filter{}, 0)
	defer b.Close()
	a.Close()
	r.Emit(Event{Kind: KindSessionCreated, Message: "still-live"})
	if got := recv(t, b); got.Message != "still-live" {
		t.Fatalf("surviving watch got %+v", got)
	}
}

func TestWatchNeverReadingSubscriberDoesNotBlockEmit(t *testing.T) {
	// A subscriber that never reads must not slow Emit. The queue is
	// tiny so overflow starts immediately; the race build catches any
	// shared-state mistake, and the wall-clock bound catches a block.
	r := NewRing(1000)
	w := r.Watch(Filter{}, 1)
	defer w.Close()

	const n = 5000
	start := time.Now()
	for i := 0; i < n; i++ {
		r.Emit(Event{Kind: KindSessionCreated})
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("%d emits against a stalled watch took %s", n, el)
	}
	dropped, _ := w.Gap()
	if dropped != n-1 {
		t.Fatalf("dropped=%d, want %d (queue of one keeps only the newest)", dropped, n-1)
	}
	if got := recv(t, w); got.Seq != n {
		t.Fatalf("stalled watch holds Seq %d, want the newest %d", got.Seq, n)
	}
}

func TestWatchConcurrentEmitReadClose(t *testing.T) {
	// Emitters, a reader, and a closer all at once; the race build is
	// the assertion. Delivery must stay in Seq order for the reader.
	r := NewRing(500)
	w := r.Watch(Filter{}, 64)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 300; j++ {
				r.Emit(Event{Kind: KindSessionCreated})
			}
		}()
	}
	var last uint64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range w.Events() {
			if ev.Seq <= last {
				t.Errorf("out of order: %d after %d", ev.Seq, last)
			}
			last = ev.Seq
		}
	}()
	wg.Wait()
	w.Close()
	<-done
	if last == 0 {
		t.Fatal("reader saw nothing")
	}
}
