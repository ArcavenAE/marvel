package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/tmux"
	"github.com/arcavenae/marvel/internal/usage"
)

// runFixture is a claude stream-json run with one metered request whose
// final result line names one refused tool call.
const runFixture = `{"type":"system","subtype":"init","session_id":"run-1","cwd":"/tmp","model":"claude-haiku-4-5"}
{"type":"assistant","session_id":"run-1","message":{"id":"msg_run1","model":"claude-haiku-4-5","role":"assistant","content":[{"type":"text","text":"board refreshed"}],"usage":{"input_tokens":1200,"output_tokens":13}}}
{"type":"result","subtype":"success","is_error":false,"session_id":"run-1","num_turns":1,"stop_reason":"end_turn","result":"board refreshed","total_cost_usd":0.0421,"usage":{"input_tokens":1200,"output_tokens":13},"modelUsage":{"claude-haiku-4-5":{"inputTokens":1200,"outputTokens":13,"costUSD":0.0421}},"permission_denials":[{"tool_name":"Bash","tool_use_id":"toolu_1"}]}
`

// exitingHarness replays runFixture and then exits with code, so a run
// can end in either outcome after a full stream.
func exitingHarness(t *testing.T, code int) string {
	t.Helper()
	dir := t.TempDir()
	fixture := filepath.Join(dir, "transcript.ndjson")
	if err := os.WriteFile(fixture, []byte(runFixture), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	script := filepath.Join(dir, "stub-claude")
	body := fmt.Sprintf("#!/bin/sh\ncat %q\nexit %d\n", fixture, code)
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return script
}

func runTestSchedule() *api.SchedulePolicy {
	return &api.SchedulePolicy{
		Cron: "17 6 * * *", Timezone: "Etc/UTC",
		Concurrency: api.ScheduleConcurrencyForbid, OnFailure: api.ScheduleOnFailureWait,
		ActiveDeadline: 45 * time.Minute, StaleAfter: 30 * time.Hour,
		History: &api.ScheduleHistory{Succeeded: 3, Failed: 3},
	}
}

// reapUntilGone polls ReapDead until no session in keys holds a pane.
func reapUntilGone(t *testing.T, mgr *Manager, store *api.Store, keys ...string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		mgr.ReapDead()
		done := true
		for _, k := range keys {
			if s, err := store.GetSession(k); err != nil || s.PaneID != "" {
				done = false
			}
		}
		if done {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("sessions %v were not reaped within 15s", keys)
}

// TestReapRecordsScheduledRuns is design section 5's run record, written
// when a scheduled role's run ends: outcome, exit status, times, the
// metered token total, the denial count and the result text. An
// unscheduled headless role in the same pass records nothing, and the
// run.* events carry status but never the result text.
func TestReapRecordsScheduledRuns(t *testing.T) {
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

	ws := "test-run-record"
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })
	if err := store.CreateWorkspace(&api.Workspace{Name: ws}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	okCmd := exitingHarness(t, 0)
	failCmd := exitingHarness(t, 3)
	rt := func(cmd string) api.Runtime {
		return api.Runtime{Name: "claude", Command: cmd, Mode: api.RuntimeModeHeadless, Prompt: "refresh"}
	}
	if err := store.CreateTeam(&api.Team{
		Name: "timers", Workspace: ws,
		Roles: []api.Role{
			{Name: "refresh", Replicas: 1, Runtime: rt(okCmd), Schedule: runTestSchedule()},
			{Name: "broken", Replicas: 1, Runtime: rt(failCmd), Schedule: runTestSchedule()},
			{Name: "plain", Replicas: 1, Runtime: rt(okCmd)},
		},
	}); err != nil {
		t.Fatalf("create team: %v", err)
	}

	before := time.Now().UTC()
	cmds := map[string]string{"refresh": okCmd, "broken": failCmd, "plain": okCmd}
	var keys []string
	for _, role := range []string{"refresh", "broken", "plain"} {
		sess := &api.Session{
			Name: "timers-" + role + "-g1-0", Workspace: ws, Team: "timers", Role: role,
			Runtime: rt(cmds[role]),
		}
		if err := mgr.Create(sess); err != nil {
			t.Fatalf("create %s: %v", role, err)
		}
		keys = append(keys, sess.Key())
	}
	reapUntilGone(t, mgr, store, keys...)

	ok, found := store.GetScheduleStatus(ws + "/timers/refresh")
	if !found || len(ok.History) != 1 {
		t.Fatalf("refresh status = %+v (found %v), want one run", ok, found)
	}
	r := ok.History[0]
	if r.Session != keys[0] || r.Outcome != api.RunSucceeded || r.ExitStatus != "0" {
		t.Fatalf("refresh run = %+v, want succeeded with exit 0 for %s", r, keys[0])
	}
	if r.StartedAt.Before(before.Add(-time.Second)) || r.EndedAt.Before(r.StartedAt) {
		t.Fatalf("refresh run times: started %v ended %v (test began %v)", r.StartedAt, r.EndedAt, before)
	}
	if r.Result != "board refreshed" || r.PermissionDenials != 1 {
		t.Fatalf("refresh run result %q denials %d, want the final message and 1 denial", r.Result, r.PermissionDenials)
	}
	if !r.Tokens.Metered || r.Tokens.Out != 13 || r.Tokens.Prompt == 0 || !r.Tokens.CostReported {
		t.Fatalf("refresh run tokens = %+v, want metered spend from the stream", r.Tokens)
	}
	if !ok.LastSucceededAt.Equal(r.EndedAt) {
		t.Fatalf("last succeeded = %v, want the run's end %v", ok.LastSucceededAt, r.EndedAt)
	}

	bad, found := store.GetScheduleStatus(ws + "/timers/broken")
	if !found || len(bad.History) != 1 || bad.History[0].Outcome != api.RunFailed || bad.History[0].ExitStatus != "3" {
		t.Fatalf("broken status = %+v (found %v), want one failed run with exit 3", bad, found)
	}
	if !bad.LastSucceededAt.IsZero() {
		t.Fatalf("a failed run set last succeeded: %v", bad.LastSucceededAt)
	}

	if _, found := store.GetScheduleStatus(ws + "/timers/plain"); found {
		t.Fatal("an unscheduled role got a run record")
	}

	succ := ring.Snapshot(events.Filter{Kind: events.KindRunSucceeded}, 0)
	fail := ring.Snapshot(events.Filter{Kind: events.KindRunFailed}, 0)
	if len(succ) != 1 || succ[0].Role != "refresh" || len(fail) != 1 || fail[0].Role != "broken" {
		t.Fatalf("run.succeeded %+v, run.failed %+v: want one each for refresh and broken", succ, fail)
	}
	for _, ev := range append(succ, fail...) {
		if strings.Contains(ev.Message, "board refreshed") {
			t.Fatalf("a run event carried the result text: %q", ev.Message)
		}
	}
}

// TestDeleteRecordsACancelledRun is the operator's ruling on #431 default
// 6: deleting a live run of a scheduled role records it as cancelled,
// with what the stream had said so far, and emits run.cancelled.
// Deleting a run that already ended records nothing more, and deleting a
// live unscheduled session records nothing.
func TestDeleteRecordsACancelledRun(t *testing.T) {
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

	ws := "test-run-cancelled"
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })
	if err := store.CreateWorkspace(&api.Workspace{Name: ws}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	// Speaks its first message, then stays alive until killed.
	dir := t.TempDir()
	fixture := filepath.Join(dir, "transcript.ndjson")
	head := strings.SplitAfterN(runFixture, "\n", 3)
	if err := os.WriteFile(fixture, []byte(head[0]+head[1]), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	slow := filepath.Join(dir, "stub-claude")
	if err := os.WriteFile(slow, []byte(fmt.Sprintf("#!/bin/sh\ncat %q\nexec sleep 60\n", fixture)), 0o700); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	rt := api.Runtime{Name: "claude", Command: slow, Mode: api.RuntimeModeHeadless, Prompt: "refresh"}
	if err := store.CreateTeam(&api.Team{
		Name: "timers", Workspace: ws,
		Roles: []api.Role{
			{Name: "refresh", Replicas: 1, Runtime: rt, Schedule: runTestSchedule()},
			{Name: "plain", Replicas: 1, Runtime: rt},
		},
	}); err != nil {
		t.Fatalf("create team: %v", err)
	}
	live := &api.Session{Name: "timers-refresh-g1-0", Workspace: ws, Team: "timers", Role: "refresh", Runtime: rt}
	plain := &api.Session{Name: "timers-plain-g1-0", Workspace: ws, Team: "timers", Role: "plain", Runtime: rt}
	for _, s := range []*api.Session{live, plain} {
		if err := mgr.Create(s); err != nil {
			t.Fatalf("create %s: %v", s.Name, err)
		}
	}
	waitForKind(t, ring, events.KindAgentMessageCompleted, 15*time.Second)
	// Both stubs must have spoken before the kill, or the tail is empty.
	deadline := time.Now().Add(10 * time.Second)
	for len(ring.Snapshot(events.Filter{Kind: events.KindAgentMessageCompleted}, 0)) < 2 && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}

	if err := mgr.Delete(live.Key()); err != nil {
		t.Fatalf("delete live run: %v", err)
	}
	if err := mgr.Delete(plain.Key()); err != nil {
		t.Fatalf("delete plain session: %v", err)
	}

	st, ok := store.GetScheduleStatus(ws + "/timers/refresh")
	if !ok || len(st.History) != 1 {
		t.Fatalf("status after delete = %+v (found %v), want one run", st, ok)
	}
	r := st.History[0]
	if r.Outcome != api.RunCancelled || r.Session != live.Key() || r.Result != "board refreshed" {
		t.Fatalf("cancelled run = %+v, want outcome cancelled with the message so far", r)
	}
	if !st.LastSucceededAt.IsZero() {
		t.Fatalf("a cancelled run set last succeeded: %v", st.LastSucceededAt)
	}
	if _, found := store.GetScheduleStatus(ws + "/timers/plain"); found {
		t.Fatal("deleting an unscheduled session recorded a run")
	}
	cancelled := ring.Snapshot(events.Filter{Kind: events.KindRunCancelled}, 0)
	if len(cancelled) != 1 || cancelled[0].Role != "refresh" || strings.Contains(cancelled[0].Message, "board refreshed") {
		t.Fatalf("run.cancelled = %+v, want one status-only event for refresh", cancelled)
	}
}

// TestDeleteOfAnEndedRunRecordsNothingMore: a run that already ended was
// recorded at reap; deleting its row later is not a second run.
func TestDeleteOfAnEndedRunRecordsNothingMore(t *testing.T) {
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(store, driver)
	mgr.StreamDir = filepath.Join(t.TempDir(), "streams")
	ws := "test-run-ended-delete"
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })
	if err := store.CreateWorkspace(&api.Workspace{Name: ws}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	rt := api.Runtime{Name: "claude", Command: exitingHarness(t, 0), Mode: api.RuntimeModeHeadless, Prompt: "refresh"}
	if err := store.CreateTeam(&api.Team{
		Name: "timers", Workspace: ws,
		Roles: []api.Role{{Name: "refresh", Replicas: 1, Runtime: rt, Schedule: runTestSchedule()}},
	}); err != nil {
		t.Fatalf("create team: %v", err)
	}
	sess := &api.Session{Name: "timers-refresh-g1-0", Workspace: ws, Team: "timers", Role: "refresh", Runtime: rt}
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create: %v", err)
	}
	reapUntilGone(t, mgr, store, sess.Key())
	if err := mgr.Delete(sess.Key()); err != nil {
		t.Fatalf("delete ended run: %v", err)
	}
	st, _ := store.GetScheduleStatus(ws + "/timers/refresh")
	if len(st.History) != 1 || st.History[0].Outcome != api.RunSucceeded {
		t.Fatalf("history = %+v, want only the succeeded run from reap", st.History)
	}
}

// TestDeleteOfAnEndedUnreapedRunIsClassifiedLikeReap: a headless run that
// exited keeps its PaneID until the next reap, so a delete in that window
// sees a pane but not a live run. It is recorded by its exit status, as
// reap would have: exit 0 succeeded (and moves last succeeded), non-zero
// failed. It is never "cancelled" (marvel#531 review).
func TestDeleteOfAnEndedUnreapedRunIsClassifiedLikeReap(t *testing.T) {
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
			ws := "test-run-del-" + tc.name
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
			sess := &api.Session{Name: "timers-refresh-g1-0", Workspace: ws, Team: "timers", Role: "refresh", Runtime: rt}
			if err := mgr.Create(sess); err != nil {
				t.Fatalf("create: %v", err)
			}
			// Wait for the pane to die, and do NOT reap: the row keeps its
			// PaneID, which is the window this test is about.
			got, err := store.GetSession(sess.Key())
			if err != nil {
				t.Fatalf("get session: %v", err)
			}
			kept := waitForExitStatus(t, driver, got.PaneID)
			wantOutcome, wantExit := tc.outcome, tc.exit
			if !kept {
				// A lost status reads as unknown, which is a failed run.
				wantOutcome, wantExit = api.RunFailed, ""
			}
			if cur, _ := store.GetSession(sess.Key()); cur.PaneID == "" {
				t.Fatal("precondition: the row lost its PaneID before the delete")
			}

			if err := mgr.Delete(sess.Key()); err != nil {
				t.Fatalf("delete: %v", err)
			}
			st, ok := store.GetScheduleStatus(ws + "/timers/refresh")
			if !ok || len(st.History) != 1 {
				t.Fatalf("status = %+v (found %v), want one run", st, ok)
			}
			if r := st.History[0]; r.Outcome != wantOutcome || r.ExitStatus != wantExit {
				t.Fatalf("run = %+v, want outcome %s with exit %q", r, wantOutcome, wantExit)
			}
			if gotSucceeded := !st.LastSucceededAt.IsZero(); gotSucceeded != (wantOutcome == api.RunSucceeded) {
				t.Fatalf("last succeeded set = %v for outcome %s", gotSucceeded, wantOutcome)
			}
			if n := len(ring.Snapshot(events.Filter{Kind: events.KindRunCancelled}, 0)); n != 0 {
				t.Fatalf("run.cancelled events = %d, want 0", n)
			}
		})
	}
}

// TestReapAfterAFailedKillRecordsACancelledRun: a delete whose kill did
// not take left KillError on the row; when the pane is later found dead,
// the reap finishes that delete. The kill was asked for, so the run is
// cancelled, with the exit status the pane reported, not failed or
// succeeded by that status.
func TestReapAfterAFailedKillRecordsACancelledRun(t *testing.T) {
	skipIfNoTmux(t)

	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := NewManager(store, driver)
	mgr.StreamDir = filepath.Join(t.TempDir(), "streams")
	ws := "test-run-reap-killerr"
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })
	if err := store.CreateWorkspace(&api.Workspace{Name: ws}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	rt := api.Runtime{Name: "claude", Command: exitingHarness(t, 0), Mode: api.RuntimeModeHeadless, Prompt: "refresh"}
	if err := store.CreateTeam(&api.Team{
		Name: "timers", Workspace: ws,
		Roles: []api.Role{{Name: "refresh", Replicas: 1, Runtime: rt, Schedule: runTestSchedule()}},
	}); err != nil {
		t.Fatalf("create team: %v", err)
	}
	sess := &api.Session{Name: "timers-refresh-g1-0", Workspace: ws, Team: "timers", Role: "refresh", Runtime: rt}
	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.State = api.SessionFailed
		live.KillError = "kill-pane: refused"
		return nil
	}); err != nil {
		t.Fatalf("mark kill-failed: %v", err)
	}
	wantExit := "0"
	if cur, err := store.GetSession(sess.Key()); err != nil {
		t.Fatalf("get session: %v", err)
	} else if !waitForExitStatus(t, driver, cur.PaneID) {
		wantExit = ""
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		mgr.ReapDead()
		if _, err := store.GetSession(sess.Key()); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the reap never finished the delete")
		}
		time.Sleep(25 * time.Millisecond)
	}
	st, ok := store.GetScheduleStatus(ws + "/timers/refresh")
	if !ok || len(st.History) != 1 {
		t.Fatalf("status = %+v (found %v), want one run", st, ok)
	}
	if r := st.History[0]; r.Outcome != api.RunCancelled || r.ExitStatus != wantExit {
		t.Fatalf("run = %+v, want cancelled with the pane's exit %q", r, wantExit)
	}
	if !st.LastSucceededAt.IsZero() {
		t.Fatalf("a cancelled run set last succeeded: %v", st.LastSucceededAt)
	}
}

// waitForExitStatus waits for the pane to die and reports whether tmux kept
// its exit status. Below tmux 3.5 a dead pane's status is lossy, and an empty
// one is read as unknown on purpose (a failed run), so the caller asserts the
// degraded contract there instead of skipping: the delete and reap
// classification stays covered on every tmux. A tmux that should keep the
// status (3.5 and later) and does not fails the test, so a silent green on a
// new tmux is impossible.
func waitForExitStatus(t *testing.T, driver *tmux.Driver, paneID string) (kept bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var deadSince time.Time
	for {
		st, _ := driver.PaneStatus(paneID)
		switch {
		case st.Exists && st.Dead && st.ExitStatus != "":
			return true
		case st.Exists && st.Dead:
			if deadSince.IsZero() {
				deadSince = time.Now()
			} else if time.Since(deadSince) > 3*time.Second {
				out, _ := exec.Command("tmux", "-V").Output()
				if statusExpected(string(out)) {
					t.Fatalf("%s lost a dead pane's exit status, which 3.5 and later keep", strings.TrimSpace(string(out)))
				}
				t.Logf("%s loses a dead pane's exit status: asserting the unknown-status contract", strings.TrimSpace(string(out)))
				return false
			}
		case time.Now().After(deadline):
			t.Fatal("pane never died")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// statusExpected reports whether `tmux -V` output names a tmux that keeps a
// dead pane's exit status (3.5 and later). An unparseable version is treated
// as expected, so a surprise fails loudly instead of degrading quietly.
func statusExpected(version string) bool {
	m := regexp.MustCompile(`(\d+)\.(\d+)`).FindStringSubmatch(version)
	if m == nil {
		return true
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major > 3 || (major == 3 && minor >= 5)
}

func TestStatusExpectedFollowsTheTmuxVersion(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"tmux 3.4", false},
		{"tmux 3.5", true},
		{"tmux 3.5a", true},
		{"tmux 3.7b", true},
		{"tmux 2.9a", false},
		{"tmux 4.0", true},
		{"tmux next-3.6", true},
		{"tmux master", true},
		{"", true},
	} {
		if got := statusExpected(tc.in); got != tc.want {
			t.Errorf("statusExpected(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
