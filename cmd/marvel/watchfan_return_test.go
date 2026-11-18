package main

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// Back from an in-place child, the watch across clusters polls every cluster
// again. A fetch that began before the child answers after it, and its rows
// were read before the return, so they must not replace "refreshing...", and
// that answer must not clear it. The single-cluster loop drops such a fetch
// (staleInflight); this is the same rule for each cluster.
func TestFanReturnDropsAFetchStartedBeforeTheChild(t *testing.T) {
	t.Setenv("TMUX", "")
	rec := newHandoffRec()
	r := newFanRigPrep(t, 200, 0, time.Hour, time.Hour,
		[]fanWatchCluster{{name: "a"}, {name: "b"}},
		func(ws *watchSort) {
			ws.term = rec
			ws.exec = func(argv []string) error { rec.ran(argv); return nil }
		},
		func(cfg *fanWatchConfig) {
			cfg.status = func(string, time.Duration) (attachTarget, bool) {
				return attachTarget{Socket: handoffSocket}, true
			}
			cfg.statusBudget = time.Second
		})
	r.waitStarted("a", 1)
	r.waitStarted("b", 1)

	// b answers and holds a row Enter can open; a's fetch 1 stays in flight.
	r.gate(fanKey{"b", 1}) <- fanFetch{
		rec:  clusterRecord{Cluster: "b", Outcome: outcomeAnswered, Cause: "1 sessions"},
		rows: []api.Session{{Workspace: "w", Name: "b-before", PaneID: "%5"}},
	}
	r.frame("b's row", contains("b-before"))

	r.keys <- 'j'
	r.keys <- 13
	rec.waitUnparked(t)
	r.waitStarted("b", 2) // the return polls every cluster that is free

	// a's fetch 1 began before the child. It answers now.
	r.answer("a", 1, "a-before")
	e := r.waitEvent("a's fetch 1", func(e fanEvent) bool { return e.Kind == "result" && e.Cluster == "a" && e.Seq == 1 })
	if e.Applied {
		t.Error("a's fetch 1 began before the child and was applied after the return")
	}
	f := r.redraw()
	if strings.Contains(f, "a-before") {
		t.Errorf("rows read before the child replaced refreshing...:\n%s", f)
	}
	if !strings.Contains(f, "refreshing...") {
		t.Errorf("a pre-return answer cleared refreshing...:\n%s", f)
	}

	// a is polled again, after the return.
	r.waitStarted("a", 2)
	r.answer("a", 2, "a-after")
	f = r.frame("a's fresh row", contains("a-after"))
	if strings.Contains(f, "refreshing...") {
		t.Errorf("refreshing... stayed after a post-return fetch answered:\n%s", f)
	}
	if strings.Contains(f, "a-before") {
		t.Errorf("rows read before the child are on screen:\n%s", f)
	}
}
