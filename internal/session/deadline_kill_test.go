package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	rtevents "github.com/arcavenae/marvel/internal/runtime/events"
	"github.com/arcavenae/marvel/internal/tmux"
	"github.com/arcavenae/marvel/internal/usage"
)

// scheduledRecorder is a manager with a store, a scheduled role and an
// event ring, and no driver: enough to call the run recorder directly.
func scheduledRecorder(t *testing.T, ws, runtimeName string) (*Manager, *api.Store, *events.Ring, api.Session) {
	t.Helper()
	store := api.NewStore()
	ring := events.NewRing(100)
	mgr := &Manager{store: store, Events: ring}
	if err := store.CreateWorkspace(&api.Workspace{Name: ws}); err != nil {
		t.Fatal(err)
	}
	rt := api.Runtime{Name: runtimeName, Command: runtimeName, Mode: api.RuntimeModeHeadless, Prompt: "refresh"}
	sched := runTestSchedule()
	if err := store.CreateTeam(&api.Team{
		Name: "timers", Workspace: ws,
		Roles: []api.Role{{Name: "refresh", Replicas: 1, Runtime: rt, Schedule: sched}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateScheduleStatus(ws+"/timers/refresh", func(st *api.ScheduleStatus) bool {
		st.Firing = "f1"
		return true
	}); err != nil {
		t.Fatal(err)
	}
	sess := api.Session{Name: "timers-refresh-g1-0", Workspace: ws, Team: "timers", Role: "refresh", Runtime: rt, Firing: "f1", CreatedAt: time.Now().UTC().Add(-time.Hour)}
	return mgr, store, ring, sess
}

func kinds(ring *events.Ring, k events.Kind) int {
	return len(ring.Snapshot(events.Filter{Kind: k}, 0))
}

// A goose run its wrapper ended at the wall-clock limit is a failed run that
// raises run.deadline instead of run.failed, once. The end reason is the
// key: the idle limit exits the same way and stays run.failed.
func TestGooseWallClockEndRaisesRunDeadline(t *testing.T) {
	tests := []struct {
		reason       string
		wantDeadline int
		wantFailed   int
	}{
		{"watchdog: wall-clock", 1, 0},
		{"watchdog: idle", 0, 1},
		{"", 0, 1},
	}
	for _, tc := range tests {
		t.Run(tc.reason, func(t *testing.T) {
			mgr, store, ring, sess := scheduledRecorder(t, "test-goose-"+strings.NewReplacer(" ", "-", ":", "").Replace(tc.reason), "goose")
			mgr.recordScheduledRun(sess, api.RunFailed, "124", runTail{endReason: tc.reason})

			st, _ := store.GetScheduleStatus(sess.Workspace + "/timers/refresh")
			if len(st.History) != 1 || st.History[0].Outcome != api.RunFailed {
				t.Fatalf("history %+v, want one failed record", st.History)
			}
			if st.Attempts != 1 || !st.Settled {
				t.Fatalf("firing %+v, want one attempt and the firing settled", st)
			}
			if n := kinds(ring, events.KindRunDeadline); n != tc.wantDeadline {
				t.Fatalf("run.deadline = %d, want %d", n, tc.wantDeadline)
			}
			if n := kinds(ring, events.KindRunFailed); n != tc.wantFailed {
				t.Fatalf("run.failed = %d, want %d", n, tc.wantFailed)
			}
			if n := kinds(ring, events.KindRunCancelled); n != 0 {
				t.Fatalf("run.cancelled = %d, want 0", n)
			}
		})
	}
}

// The wall-clock reason alone does not make another harness's failure a
// deadline: only a goose run's watchdog raises it.
func TestWallClockReasonOnAnotherHarnessStaysRunFailed(t *testing.T) {
	mgr, _, ring, sess := scheduledRecorder(t, "test-wallclock-other", "claude")
	mgr.recordScheduledRun(sess, api.RunFailed, "124", runTail{endReason: "watchdog: wall-clock"})
	if n := kinds(ring, events.KindRunDeadline); n != 0 {
		t.Fatalf("run.deadline = %d, want 0 for a claude run", n)
	}
	if n := kinds(ring, events.KindRunFailed); n != 1 {
		t.Fatalf("run.failed = %d, want 1", n)
	}
}

// The drain keeps the end reason the harness gave, for the recorder to read.
func TestUsageDrainKeepsTheEndReason(t *testing.T) {
	d := &usageDrain{}
	d.note(rtevents.Event{Data: rtevents.SessionEndedData{Reason: "watchdog: wall-clock", ExitCode: 124}})
	if d.tail.endReason != "watchdog: wall-clock" {
		t.Fatalf("end reason = %q", d.tail.endReason)
	}
}

// A deadline kill whose pane status cannot be read is still a failed run
// with run.deadline. Delete's reading of an unreadable status is a live
// run that was cancelled, which on this path would turn the kill back into
// an operator cancel and leave retries and on_failure out of it.
func TestKillPastDeadlineWithAnUnreadablePaneStatusIsFailed(t *testing.T) {
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	ring := events.NewRing(400)
	mgr := NewManager(store, driver)
	mgr.Events = ring
	mgr.Usage = usage.New(store, usage.NewResolver(usage.DefaultTable()))
	mgr.StreamDir = filepath.Join(t.TempDir(), "streams")
	mgr.paneStatus = func(string) (tmux.PaneStatus, error) {
		return tmux.PaneStatus{}, fmt.Errorf("pane status unavailable")
	}

	ws := "test-deadline-unreadable"
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })
	if err := store.CreateWorkspace(&api.Workspace{Name: ws}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	slow := filepath.Join(dir, "stub-claude")
	if err := os.WriteFile(slow, []byte("#!/bin/sh\nexec sleep 60\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	rt := api.Runtime{Name: "claude", Command: slow, Mode: api.RuntimeModeHeadless, Prompt: "refresh"}
	if err := store.CreateTeam(&api.Team{
		Name: "timers", Workspace: ws,
		Roles: []api.Role{{Name: "refresh", Replicas: 1, Runtime: rt, Schedule: runTestSchedule()}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateScheduleStatus(ws+"/timers/refresh", func(st *api.ScheduleStatus) bool {
		st.Firing = "f1"
		return true
	}); err != nil {
		t.Fatal(err)
	}
	live := &api.Session{Name: "timers-refresh-g1-0", Workspace: ws, Team: "timers", Role: "refresh", Runtime: rt, Firing: "f1"}
	if err := mgr.Create(live); err != nil {
		t.Fatal(err)
	}

	if err := mgr.KillPastDeadline(live.Key()); err != nil {
		t.Fatalf("kill past deadline: %v", err)
	}
	st, _ := store.GetScheduleStatus(ws + "/timers/refresh")
	if len(st.History) != 1 || st.History[0].Outcome != api.RunFailed {
		t.Fatalf("history %+v, want one failed record", st.History)
	}
	if st.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", st.Attempts)
	}
	if n := kinds(ring, events.KindRunDeadline); n != 1 {
		t.Fatalf("run.deadline = %d, want 1", n)
	}
	if n := kinds(ring, events.KindRunCancelled); n != 0 {
		t.Fatalf("run.cancelled = %d, want 0", n)
	}
}

// A pane that had already ended when the deadline check ran was not killed,
// so its own exit decides the record, as Delete classifies it: exit 0 is
// succeeded, anything else failed, and neither raises run.deadline. Moving
// the deadline case above the ended cases would record both as a deadline
// failure.
func TestKillPastDeadlineOfAnEndedUnreapedRunIsClassifiedLikeReap(t *testing.T) {
	skipIfNoTmux(t)

	for _, tc := range []struct {
		name    string
		code    int
		outcome api.RunOutcome
		exit    string
	}{
		{"exit0", 0, api.RunSucceeded, "0"},
		{"exit3", 3, api.RunFailed, "3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := api.NewStore()
			driver, err := tmux.NewDriver()
			if err != nil {
				t.Fatalf("new driver: %v", err)
			}
			ring := events.NewRing(400)
			mgr := NewManager(store, driver)
			mgr.Events = ring
			mgr.StreamDir = filepath.Join(t.TempDir(), "streams")
			ws := "test-deadline-ended-" + tc.name
			t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })
			if err := store.CreateWorkspace(&api.Workspace{Name: ws}); err != nil {
				t.Fatalf("create workspace: %v", err)
			}
			rt := api.Runtime{Name: "claude", Command: exitingHarness(t, tc.code), Mode: api.RuntimeModeHeadless, Prompt: "refresh"}
			if err := store.CreateTeam(&api.Team{
				Name: "timers", Workspace: ws,
				Roles: []api.Role{{Name: "refresh", Replicas: 1, Runtime: rt, Schedule: runTestSchedule()}},
			}); err != nil {
				t.Fatalf("create team: %v", err)
			}
			if _, err := store.UpdateScheduleStatus(ws+"/timers/refresh", func(st *api.ScheduleStatus) bool {
				st.Firing = "f1"
				return true
			}); err != nil {
				t.Fatal(err)
			}
			sess := &api.Session{Name: "timers-refresh-g1-0", Workspace: ws, Team: "timers", Role: "refresh", Runtime: rt, Firing: "f1"}
			if err := mgr.Create(sess); err != nil {
				t.Fatalf("create: %v", err)
			}
			// Wait for the pane to die, and do not reap: the row keeps its
			// PaneID, which is the window a deadline check can land in.
			got, err := store.GetSession(sess.Key())
			if err != nil {
				t.Fatalf("get session: %v", err)
			}
			wantOutcome, wantExit := tc.outcome, tc.exit
			if !waitForExitStatus(t, driver, got.PaneID) {
				// A lost status reads as unknown, which is a failed run.
				wantOutcome, wantExit = api.RunFailed, ""
			}
			if cur, _ := store.GetSession(sess.Key()); cur.PaneID == "" {
				t.Fatal("precondition: the row lost its PaneID before the kill")
			}

			if err := mgr.KillPastDeadline(sess.Key()); err != nil {
				t.Fatalf("kill past deadline: %v", err)
			}
			st, ok := store.GetScheduleStatus(ws + "/timers/refresh")
			if !ok || len(st.History) != 1 {
				t.Fatalf("status = %+v (found %v), want one run", st, ok)
			}
			if r := st.History[0]; r.Outcome != wantOutcome || r.ExitStatus != wantExit {
				t.Fatalf("run = %+v, want outcome %s with exit %q", r, wantOutcome, wantExit)
			}
			if n := kinds(ring, events.KindRunDeadline); n != 0 {
				t.Fatalf("run.deadline events = %d, want 0 for a run that had already ended", n)
			}
			if n := kinds(ring, events.KindRunCancelled); n != 0 {
				t.Fatalf("run.cancelled events = %d, want 0", n)
			}
			wantKind := events.KindRunSucceeded
			if wantOutcome == api.RunFailed {
				wantKind = events.KindRunFailed
			}
			if n := kinds(ring, wantKind); n != 1 {
				t.Fatalf("%s events = %d, want 1", wantKind, n)
			}
		})
	}
}

// Only a failed goose run can be a deadline failure. The same wall-clock
// end reason on a run recorded succeeded or cancelled keeps its own event,
// so the outcome == RunFailed guard in recordScheduledRun is pinned: with it
// removed or flipped, one of these rows raises run.deadline or loses its
// own event.
func TestOnlyAFailedGooseRunRaisesRunDeadline(t *testing.T) {
	tests := []struct {
		outcome api.RunOutcome
		want    events.Kind
	}{
		{api.RunSucceeded, events.KindRunSucceeded},
		{api.RunCancelled, events.KindRunCancelled},
		{api.RunFailed, events.KindRunDeadline},
	}
	for _, tc := range tests {
		t.Run(string(tc.outcome), func(t *testing.T) {
			mgr, _, ring, sess := scheduledRecorder(t, "test-goose-guard-"+string(tc.outcome), "goose")
			mgr.recordScheduledRun(sess, tc.outcome, "124", runTail{endReason: "watchdog: wall-clock"})
			for _, k := range []events.Kind{events.KindRunSucceeded, events.KindRunFailed, events.KindRunCancelled, events.KindRunDeadline} {
				want := 0
				if k == tc.want {
					want = 1
				}
				if n := kinds(ring, k); n != want {
					t.Errorf("%s events = %d, want %d for a %s run", k, n, want, tc.outcome)
				}
			}
		})
	}
}
