package session

import (
	"sync"
	"testing"
)

// endedRecorder collects the keys a Manager reports through SessionEnded.
type endedRecorder struct {
	mu   sync.Mutex
	keys []string
}

func (r *endedRecorder) note(key string) {
	r.mu.Lock()
	r.keys = append(r.keys, key)
	r.mu.Unlock()
}

func (r *endedRecorder) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.keys...)
}

func TestDeleteReportsTheSessionEnded(t *testing.T) {
	_, mgr, _, sess := keptRowFixture(t)
	mgr.killPane = func(string) error { return nil }
	var rec endedRecorder
	mgr.SessionEnded = rec.note

	if err := mgr.Delete(sess.Key()); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := rec.got(); len(got) != 1 || got[0] != sess.Key() {
		t.Fatalf("SessionEnded keys = %v, want [%s]", got, sess.Key())
	}
}

// A kill that did not take leaves a process that may still be running, and a
// seat being deleted should not keep its credentials while it does.
func TestDeleteReportsTheSessionEndedWhenTheKillFails(t *testing.T) {
	_, mgr, _, sess := keptRowFixture(t)
	mgr.killPane = func(string) error { return errKillRefused }
	var rec endedRecorder
	mgr.SessionEnded = rec.note

	if err := mgr.Delete(sess.Key()); err == nil {
		t.Fatal("Delete returned nil for a refused kill")
	}
	if got := rec.got(); len(got) != 1 || got[0] != sess.Key() {
		t.Fatalf("SessionEnded keys = %v, want [%s]", got, sess.Key())
	}
}

func TestReapingAGonePaneReportsTheSessionEnded(t *testing.T) {
	_, mgr, _, sess := keptRowFixture(t)
	var rec endedRecorder
	mgr.SessionEnded = rec.note

	mgr.detachInstance(sess.Key())
	if got := rec.got(); len(got) != 1 || got[0] != sess.Key() {
		t.Fatalf("SessionEnded keys = %v, want [%s]", got, sess.Key())
	}
}
