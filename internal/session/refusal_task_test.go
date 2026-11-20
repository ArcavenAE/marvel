package session

import (
	"errors"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// A placement refusal is emitted after the spawn path has deleted the session
// row, so the store has no task left to look up. The refusal event carries the
// task of the session it was raised for.
func TestPlacementRefusalEventCarriesTheSessionsTask(t *testing.T) {
	r := newContainerRig(t, "test-refusal-task", api.PlacementLocalhost, api.Role{Name: "seat", Replicas: 1})
	r.mgr.BackendOverlayDir = t.TempDir()
	widen(t, r.mgr.BackendOverlayDir)
	r.ring.SetTaskResolver(r.store.TaskOf)
	sess := r.session("sleep")
	sess.Runtime.Backend = "bedrock"
	sess.Task = "I-100"

	if err := r.mgr.Create(sess); !errors.Is(err, ErrPlacementRefused) {
		t.Fatalf("Create = %v, want an error wrapping ErrPlacementRefused", err)
	}
	var found int
	for _, ev := range r.ring.Snapshot(events.Filter{}, 0) {
		if ev.Kind != events.KindSessionPlacementRefused {
			continue
		}
		found++
		if ev.Task != "I-100" {
			t.Errorf("refusal event Task = %q, want I-100", ev.Task)
		}
	}
	if found == 0 {
		t.Fatal("no placement-refused event was emitted")
	}
}
