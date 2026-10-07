package view

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

type moveCall struct {
	session, view, path, previous, commit string
}

// OnMoved says a view moved from one commit to another, and only that: a first
// build and an unchanged refresh are not moves.
func TestKeeperCallsOnMovedOnlyWhenAViewMoves(t *testing.T) {
	r := newKRig(t)
	var calls []moveCall
	r.k.OnMoved = func(s api.Session, view, path, previous, commit string) {
		calls = append(calls, moveCall{s.Key(), view, path, previous, commit})
	}
	r.g.sha["a"] = shaOne
	views := []api.View{kView("alpha", "a", 10*time.Minute)}
	sess := r.session("seat", views...)

	r.k.Build(sess, views)
	if len(calls) != 0 {
		t.Fatalf("a first build called OnMoved: %+v", calls)
	}

	r.advance(time.Hour)
	r.k.Tick()
	if len(calls) != 0 {
		t.Fatalf("an unchanged refresh called OnMoved: %+v", calls)
	}

	r.g.sha["a"] = shaTwo
	r.advance(time.Hour)
	r.k.Tick()
	want := moveCall{sess.Key(), "alpha", filepath.Join(r.k.ViewsDir, sess.Key(), "alpha", "cur"), shaOne, shaTwo}
	if len(calls) != 1 || calls[0] != want {
		t.Fatalf("calls = %+v, want one: %+v", calls, want)
	}
}
