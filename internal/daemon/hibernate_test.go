package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/session"
	"github.com/arcavenae/marvel/internal/suspend"
	"github.com/arcavenae/marvel/internal/team"
	"github.com/arcavenae/marvel/internal/tmux"
)

func hibernateDaemon(t *testing.T, roles ...api.Role) *Daemon {
	t.Helper()
	store := api.NewStore()
	if err := store.CreateWorkspace(&api.Workspace{Name: "acme", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTeam(&api.Team{Name: "squad", Workspace: "acme", Roles: roles, Generation: 1, ConvergencePosture: api.PostureConverge}); err != nil {
		t.Fatal(err)
	}
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	mgr := session.NewManager(store, driver)
	t.Cleanup(func() { _ = mgr.CleanupWorkspace("acme") })
	ctrl := team.NewController(store, mgr)
	ring := events.NewRing(0)
	ctrl.Events = ring
	return &Daemon{store: store, teamCtrl: ctrl, sessMgr: mgr, driver: driver, events: ring}
}

func roleOn(name, command, backend string) api.Role {
	return api.Role{Name: name, Replicas: 1, Runtime: api.Runtime{Name: command, Command: command, Backend: backend}}
}

func call(t *testing.T, d *Daemon, method string, v any) (daemonResp, string) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var resp daemonResp
	r := d.handleHibernate
	if method == "team.wake" {
		r = d.handleWake
	}
	got := r(raw)
	resp.Result = got.Result
	return resp, got.Error
}

type daemonResp struct{ Result json.RawMessage }

func TestHibernateMarksTheTeamAndCountsLabels(t *testing.T) {
	d := hibernateDaemon(t, roleOn("paid", "sleep", "bedrock"), roleOn("local", "sleep", ""))
	resp, errText := call(t, d, "team.hibernate", hibernateParams{Teams: []string{"acme/squad"}, Backend: []string{"none"}, DryRun: true})
	if errText != "" {
		t.Fatal(errText)
	}
	var dry hibernateResult
	if err := json.Unmarshal(resp.Result, &dry); err != nil {
		t.Fatal(err)
	}
	if _, ok := dry.Labels["none"]; !ok || dry.Labels["bedrock"] != 0 {
		t.Fatalf("labels = %v, want only none selected", dry.Labels)
	}
	if tm, _ := d.store.GetTeam("acme/squad"); tm.Hibernate != nil {
		t.Fatal("a dry run marked the team")
	}

	if _, errText := call(t, d, "team.hibernate", hibernateParams{Teams: []string{"acme/squad"}, ExceptBackend: []string{"bedrock"}}); errText != "" {
		t.Fatal(errText)
	}
	tm, _ := d.store.GetTeam("acme/squad")
	if tm.Hibernate == nil || strings.Join(tm.Hibernate.Roles, ",") != "local" {
		t.Fatalf("mark = %+v, want the unlabeled role only", tm.Hibernate)
	}
}

func TestHibernateAndSuspendRefuseEachOther(t *testing.T) {
	d := hibernateDaemon(t, roleOn("worker", "sleep", ""))
	rec := suspend.Record{State: suspend.StateSuspended}
	d.suspendRec.Store(&rec)
	if _, errText := call(t, d, "team.hibernate", hibernateParams{All: true}); !strings.Contains(errText, "suspended") {
		t.Fatalf("hibernate during a suspend: err = %q, want a refusal naming it", errText)
	}
	d.suspendRec.Store(nil)

	if err := d.store.UpdateTeam("acme/squad", func(live *api.Team) error {
		live.Hibernate = &api.HibernateMark{State: api.HibernateHibernated, Since: time.Now().UTC()}
		live.HibernateRequests = map[string]api.ShiftRequest{"acme/s": {Session: "acme/s", Cause: api.HandoffCauseHibernate}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	resp := d.handleSuspend(nil)
	if !strings.Contains(resp.Error, "hibernate of acme/squad is still signing off") {
		t.Fatalf("suspend during a hibernate: err = %q, want a refusal naming the team", resp.Error)
	}
}

func TestWakeRefusedByAFailedCheckUntilItIsSkipped(t *testing.T) {
	d := hibernateDaemon(t, roleOn("worker", "no-such-harness-binary-xyz", ""))
	if err := d.store.UpdateTeam("acme/squad", func(live *api.Team) error {
		live.Hibernate = &api.HibernateMark{State: api.HibernateHibernated, Since: time.Now().UTC()}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, errText := call(t, d, "team.wake", wakeParams{All: true}); !strings.Contains(errText, "harness-binary") || !strings.Contains(errText, "no-such-harness-binary-xyz") {
		t.Fatalf("wake err = %q, want the failed check named", errText)
	}
	if tm, _ := d.store.GetTeam("acme/squad"); tm.Hibernate.State != api.HibernateHibernated {
		t.Fatal("a refused wake changed the mark")
	}
	if _, errText := call(t, d, "team.wake", wakeParams{All: true, SkipChecks: []string{"bogus"}}); !strings.Contains(errText, "unknown check") {
		t.Fatalf("an unknown check name: err = %q", errText)
	}
	resp, errText := call(t, d, "team.wake", wakeParams{All: true, SkipChecks: []string{"harness-binary"}})
	if errText != "" {
		t.Fatalf("a skipped check still refused: %s", errText)
	}
	var res wakeResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		t.Fatal(err)
	}
	skipped := false
	for _, c := range res.Checks {
		skipped = skipped || (c.Name == "harness-binary" && c.Skipped)
	}
	tm, _ := d.store.GetTeam("acme/squad")
	if !skipped || tm.Hibernate == nil || tm.Hibernate.State != api.HibernateWaking {
		t.Fatalf("skipped=%v mark=%+v, want the skip recorded and the team waking", skipped, tm.Hibernate)
	}
}

func TestConvergeRefusesAHibernatedTeam(t *testing.T) {
	d := hibernateDaemon(t, roleOn("worker", "sleep", ""))
	if err := d.store.UpdateTeam("acme/squad", func(live *api.Team) error {
		live.Hibernate = &api.HibernateMark{State: api.HibernateHibernated, Since: time.Now().UTC()}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	resp := d.handleConverge(json.RawMessage(`{"team_key":"acme/squad"}`))
	if !strings.Contains(resp.Error, "hibernated; run marvel wake") {
		t.Fatalf("converge err = %q, want the wake pointer", resp.Error)
	}
}

func TestDescribeShowsOpenHibernateRequestsPerSession(t *testing.T) {
	d := hibernateDaemon(t, roleOn("worker", "sleep", ""))
	if err := d.store.UpdateTeam("acme/squad", func(live *api.Team) error {
		live.Hibernate = &api.HibernateMark{State: api.HibernateHibernated, Since: time.Now().UTC()}
		live.HibernateRequests = map[string]api.ShiftRequest{
			"acme/b": {Session: "acme/b", Cause: api.HandoffCauseHibernate, RequestedAt: time.Now().UTC().Add(-time.Minute)},
			"acme/a": {Session: "acme/a", Cause: api.HandoffCauseHibernate, RequestedAt: time.Now().UTC().Add(-time.Minute), Escalated: true},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	desc, err := d.describeTeam("acme/squad")
	if err != nil {
		t.Fatal(err)
	}
	rows := desc.PendingHibernateRequests
	if len(rows) != 2 || rows[0].Session != "acme/a" || !rows[0].Escalated || rows[1].Session != "acme/b" || rows[0].Cause != "hibernate" {
		t.Fatalf("rows = %+v, want one per session, in session order, with the cause", rows)
	}
	if len(desc.PendingShiftRequests) != 0 {
		t.Fatalf("shift rows = %+v, want none", desc.PendingShiftRequests)
	}
}

func TestScaleOnAHibernatedTeamHoldsAndSaysSo(t *testing.T) {
	d := hibernateDaemon(t, roleOn("worker", "sleep", ""))
	if err := d.store.UpdateTeam("acme/squad", func(live *api.Team) error {
		live.Hibernate = &api.HibernateMark{State: api.HibernateHibernated, Since: time.Now().UTC()}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	resp := d.handleScale(json.RawMessage(`{"team_key":"acme/squad","role":"worker","replicas":2}`))
	if resp.Error != "" {
		t.Fatalf("scale refused: %s", resp.Error)
	}
	var out struct {
		Held string `json:"held"`
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	if out.Held != "held: hibernated; run marvel wake" {
		t.Fatalf("scale held = %q, want the hold line", out.Held)
	}
	tm, _ := d.store.GetTeam("acme/squad")
	if tm.Roles[0].Replicas != 2 {
		t.Fatalf("replicas = %d, want the new intent recorded", tm.Roles[0].Replicas)
	}
}

func TestHarnessCheckReadsTheBinaryOfACommandWithArguments(t *testing.T) {
	d := hibernateDaemon(t, roleOn("worker", "sleep 300", ""))
	tm, _ := d.store.GetTeam("acme/squad")
	tm.Hibernate = &api.HibernateMark{State: api.HibernateHibernated}
	if c := d.harnessCheck([]api.Team{tm}); c.Result != "passed" {
		t.Fatalf("harness check = %+v, want a command with arguments judged by its first field", c)
	}
}
