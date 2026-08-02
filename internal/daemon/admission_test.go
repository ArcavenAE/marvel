package daemon

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/admission"
	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// newHandlerDaemon builds a daemon and calls its RPC handlers directly, with
// no socket and no reconcile goroutine. The admission gates are synchronous
// handler code, so nothing here needs the listener.
func newHandlerDaemon(t *testing.T) *Daemon {
	t.Helper()
	skipIfNoTmux(t)
	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	t.Cleanup(func() {
		for _, ws := range d.store.ListWorkspaces() {
			_ = d.sessMgr.CleanupWorkspace(ws.Name)
		}
	})
	return d
}

func applyManifest(t *testing.T, d *Daemon, manifest string) Response {
	t.Helper()
	params, err := json.Marshal(map[string]any{"manifest_data": []byte(manifest)})
	if err != nil {
		t.Fatalf("marshal apply params: %v", err)
	}
	return d.handleApply(params)
}

// budgetedManifest is a team at its declared ceiling: three sleep replicas
// under a six-session budget. Deterministic, and needs no model auth.
const budgetedManifest = `
workspace:
  name: fanout
teams:
  - name: crew
    budget:
      max_sessions: 3
    roles:
      - name: crew
        replicas: 3
        runtime:
          command: sleep
          args: ["300"]
`

// TestHandleScaleRefusesOverBudget is the "refuse the declaration, not the
// spawn" contract as an assertion.
//
// Falsification: gating only the reconciler's spawn would report "scaled",
// leave Replicas at 40, show 3 sessions, and re-decide the same impossible
// deficit every 2s forever. Here the verb fails, and the store never learns
// a number that can never be true.
func TestHandleScaleRefusesOverBudget(t *testing.T) {
	d := newHandlerDaemon(t)
	if resp := applyManifest(t, d, budgetedManifest); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}

	resp := d.handleScale(mustMarshal(t, scaleParams{TeamKey: "fanout/crew", Role: "crew", Replicas: 40}))
	if resp.Error == "" {
		t.Fatal("expected an error scaling past the declared ceiling")
	}
	for _, want := range []string{"3 live", "37 requested", "max_sessions=3", "trigger=scale", "Nothing changed"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error = %q, missing %q", resp.Error, want)
		}
	}

	team, err := d.store.GetTeam("fanout/crew")
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	if team.Roles[0].Replicas != 3 {
		t.Errorf("replicas = %d after a refused scale, want 3", team.Roles[0].Replicas)
	}
	if got := len(d.events.Snapshot(events.Filter{Kind: events.KindAdmissionRefused}, 0)); got != 1 {
		t.Errorf("admission.refused events = %d, want 1", got)
	}
}

// TestHandleScaleDownIsNeverRefused: shedding sessions is how an operator
// frees headroom, so a request that adds nothing never reaches the gate.
func TestHandleScaleDownIsNeverRefused(t *testing.T) {
	d := newHandlerDaemon(t)
	if resp := applyManifest(t, d, budgetedManifest); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}

	if resp := d.handleScale(mustMarshal(t, scaleParams{TeamKey: "fanout/crew", Role: "crew", Replicas: 1})); resp.Error != "" {
		t.Fatalf("a scale-down was refused: %s", resp.Error)
	}
	team, _ := d.store.GetTeam("fanout/crew")
	if team.Roles[0].Replicas != 1 {
		t.Errorf("replicas = %d after scale-down, want 1", team.Roles[0].Replicas)
	}
}

// TestHandleScaleRefusesADeclarationOverTheCeiling covers the second door into
// an over-ceiling declaration.
//
// The spawn gate compares LIVE sessions, so it cannot see this: with a replica
// dead, live sits below declared and live+want still fits under the ceiling
// while the declared sum does not. That is the normal state during a crash
// loop, which is also when an operator reaches for scale.
//
// Falsification: without the declaration clause at the verb, this scale
// returns "scaled", sum(Replicas) goes to 4 under max_sessions=3, and R1
// (declared <= limit) stops holding. The reconciler then refuses the deficient
// role forever, since a refusal never bumps RestartCount and no backoff
// re-fires.
func TestHandleScaleRefusesADeclarationOverTheCeiling(t *testing.T) {
	d := newHandlerDaemon(t)
	if resp := applyManifest(t, d, budgetedManifest); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}

	// One replica dies without being reaped: live 2, declared 3, ceiling 3.
	sessions := d.store.ListSessionsByTeam("fanout", "crew")
	if len(sessions) != 3 {
		t.Fatalf("got %d session(s) after apply, want 3", len(sessions))
	}
	if err := d.store.UpdateSession(sessions[0].Key(), func(s *api.Session) error {
		s.State = api.SessionCrashed
		return nil
	}); err != nil {
		t.Fatalf("mark session crashed: %v", err)
	}
	if live := api.CountAlive(d.store.ListSessionsByTeam("fanout", "crew")); live != 2 {
		t.Fatalf("live = %d, want 2 (the window this gate covers)", live)
	}

	resp := d.handleScale(mustMarshal(t, scaleParams{TeamKey: "fanout/crew", Role: "crew", Replicas: 4}))
	if resp.Error == "" {
		t.Fatal("expected a scale that declares 4 under a 3-session ceiling to be refused")
	}
	for _, want := range []string{"role crew at 4 replica(s)", "declare 4 sessions", "max_sessions=3", "trigger=scale", "Nothing changed"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error = %q, missing %q", resp.Error, want)
		}
	}
	team, err := d.store.GetTeam("fanout/crew")
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	if team.Roles[0].Replicas != 3 {
		t.Errorf("replicas = %d after a refused scale, want 3", team.Roles[0].Replicas)
	}
	// Never silent: the refusal carries its arithmetic into the event ring the
	// same way the spawn gate's does.
	if got := len(d.events.Snapshot(events.Filter{Kind: events.KindAdmissionRefused}, 0)); got != 1 {
		t.Errorf("admission.refused events = %d, want 1", got)
	}

	// The same live count still admits a scale that keeps the declaration
	// under the ceiling, so the new gate refuses declarations rather than
	// scales.
	if resp := d.handleScale(mustMarshal(t, scaleParams{TeamKey: "fanout/crew", Role: "crew", Replicas: 2})); resp.Error != "" {
		t.Fatalf("a within-ceiling scale was refused: %s", resp.Error)
	}
}

// TestHandleScaleUnknownRoleReportsTheRole covers the reorder the budget gate
// forced.
//
// Falsification: the role-existence scan used to run after UpdateTeam. With
// the gate inserted in front of the mutation and the scan left where it was,
// a mistyped role name under a declared budget would report a budget error
// instead of "role not found" — the wrong diagnostic for the actual mistake.
func TestHandleScaleUnknownRoleReportsTheRole(t *testing.T) {
	d := newHandlerDaemon(t)
	if resp := applyManifest(t, d, budgetedManifest); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}

	resp := d.handleScale(mustMarshal(t, scaleParams{TeamKey: "fanout/crew", Role: "crwe", Replicas: 40}))
	if resp.Error == "" {
		t.Fatal("expected an error for an unknown role")
	}
	if !strings.Contains(resp.Error, "role crwe not found") {
		t.Errorf("error = %q, want it to name the missing role", resp.Error)
	}
	if strings.Contains(resp.Error, "max_sessions") {
		t.Errorf("error = %q, reported a budget problem for a typo", resp.Error)
	}
}

// TestHandleApplyBudgetRefusals covers both apply-time gates: the parse-time
// declaration clause (which catches the motivating fan-out before any daemon
// state exists) and the host-side pre-flight for a ceiling no role can report
// against. Neither may leave store state behind.
func TestHandleApplyBudgetRefusals(t *testing.T) {
	tests := []struct {
		name      string
		manifest  string
		wantErr   string
		workspace string
	}{
		{
			name: "a declared fan-out over the ceiling is refused at parse (toml)",
			manifest: `
[workspace]
name = "overdeclared"

[[team]]
name = "crew"

  [team.budget]
  max_sessions = 6

  [[team.role]]
  name = "crew"
  replicas = 40

    [team.role.runtime]
    command = "sleep"
    args = ["300"]
`,
			wantErr:   "declares 40 replicas across 1 role(s) but budget.max_sessions is 6",
			workspace: "overdeclared",
		},
		{
			// The same clause through the format the guide's example uses.
			// ParseManifestBytes used to validate inside each format attempt,
			// so a YAML validation failure fell through to the TOML parser and
			// the operator got "toml: line N: expected '.' or '='" instead of
			// the rule. `marvel work` sends bytes, so this was the only apply
			// path there is.
			name: "a declared fan-out over the ceiling is refused at parse (yaml)",
			manifest: `
workspace:
  name: overdeclaredyaml
teams:
  - name: crew
    budget:
      max_sessions: 6
    roles:
      - name: crew
        replicas: 40
        runtime:
          command: sleep
          args: ["300"]
`,
			wantErr:   "declares 40 replicas across 1 role(s) but budget.max_sessions is 6",
			workspace: "overdeclaredyaml",
		},
		{
			name: "a token ceiling no role can report against is refused at pre-flight",
			manifest: `
workspace:
  name: mutebudget
teams:
  - name: crew
    budget:
      max_tokens: 2000000
    roles:
      - name: crew
        replicas: 1
        runtime:
          command: sleep
          args: ["300"]
`,
			wantErr:   "no role runs a stream-capable harness in headless mode",
			workspace: "mutebudget",
		},
		{
			// The hole a mode-only pre-flight left. generic implements no
			// stream path, so this ceiling could never move off zero however
			// long the team ran.
			name: "a headless role whose harness cannot stream is still a mute gate",
			manifest: `
workspace:
  name: mutegeneric
teams:
  - name: crew
    budget:
      max_tokens: 2000000
    roles:
      - name: crew
        replicas: 1
        runtime:
          image: generic
          command: sleep
          mode: headless
          prompt: "review the diff"
`,
			wantErr:   "no role runs a stream-capable harness in headless mode",
			workspace: "mutegeneric",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			d := newHandlerDaemon(t)
			resp := applyManifest(t, d, tt.manifest)
			if resp.Error == "" {
				t.Fatal("expected apply to be refused")
			}
			if !strings.Contains(resp.Error, tt.wantErr) {
				t.Errorf("error = %q, missing %q", resp.Error, tt.wantErr)
			}
			if _, err := d.store.GetWorkspace(tt.workspace); err == nil {
				t.Errorf("a refused apply created workspace %q", tt.workspace)
			}
		})
	}
}

// TestHandleRunRespectsATeamBudget closes the hole a controller-only gate
// would leave: `marvel run` bypasses the reconciler entirely, and its --team
// can name a team that declares a budget.
func TestHandleRunRespectsATeamBudget(t *testing.T) {
	d := newHandlerDaemon(t)
	if resp := applyManifest(t, d, budgetedManifest); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}

	resp := d.handleRun(mustMarshal(t, runParams{
		Workspace: "fanout", Team: "crew", Role: "extra",
		RuntimeCommand: "sleep", RuntimeArgs: []string{"300"},
	}))
	if resp.Error == "" {
		t.Fatal("expected an ad-hoc run into a full team to be refused")
	}
	for _, want := range []string{"max_sessions=3", "trigger=run"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("error = %q, missing %q", resp.Error, want)
		}
	}
	if got := api.CountAlive(d.store.ListSessionsByTeam("fanout", "crew")); got != 3 {
		t.Errorf("live sessions = %d after a refused run, want 3", got)
	}

	// The default ad-hoc team has no Team record, so it declares no budget
	// and behaves exactly as it did before admission existed.
	resp = d.handleRun(mustMarshal(t, runParams{
		RuntimeCommand: "sleep", RuntimeArgs: []string{"300"},
	}))
	if resp.Error != "" {
		t.Fatalf("an ad-hoc run with no declared budget was refused: %s", resp.Error)
	}
}

// TestHandleGetBudgets covers the diagnostic surface: rows only for teams
// that declare a budget, and per-dimension numbers an operator can act on.
// Nothing else in marvel exposes a spend or occupancy aggregate.
func TestHandleGetBudgets(t *testing.T) {
	d := newHandlerDaemon(t)
	if resp := applyManifest(t, d, budgetedManifest); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	plain := `
workspace:
  name: fanout
teams:
  - name: plain
    roles:
      - name: worker
        replicas: 1
        runtime:
          command: sleep
          args: ["300"]
`
	if resp := applyManifest(t, d, plain); resp.Error != "" {
		t.Fatalf("apply plain: %s", resp.Error)
	}

	resp := d.handleGet(mustMarshal(t, getParams{ResourceType: "budgets"}))
	if resp.Error != "" {
		t.Fatalf("get budgets: %s", resp.Error)
	}
	var rows []admission.Row
	if err := json.Unmarshal(resp.Result, &rows); err != nil {
		t.Fatalf("unmarshal rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d row(s), want 1 (only the budgeted team): %+v", len(rows), rows)
	}
	got := rows[0]
	if got.Team != "crew" || got.Dimension != api.DimMaxSessions {
		t.Fatalf("row = %+v, want the crew team's max_sessions", got)
	}
	if got.Limit != 3 || got.Observed != 3 || got.Headroom != 0 {
		t.Errorf("row = %+v, want limit 3, observed 3, headroom 0", got)
	}
	// A team whose declared replicas equal its ceiling refuses nothing, so the
	// row reads at-ceiling. Keying refusal on zero headroom made "refusing"
	// the resting state of a healthy team and left the only
	// which-dimension-tripped surface unable to tell one from the other.
	if got.State != admission.RowAtCeiling {
		t.Errorf("state = %q, want %q at the ceiling with nothing refused", got.State, admission.RowAtCeiling)
	}
	if len(d.events.Snapshot(events.Filter{Kind: events.KindAdmissionRefused}, 0)) != 0 {
		t.Error("a team sitting at its ceiling emitted an admission.refused event")
	}

	// And what refusing looks like when a refusal is genuinely standing. The
	// out-of-band write is the only remaining door to a declared count above
	// the ceiling now that both apply and scale carry the declaration clause.
	if err := d.store.UpdateTeam("fanout/crew", func(live *api.Team) error {
		live.Roles[0].Replicas = 5
		return nil
	}); err != nil {
		t.Fatalf("set replicas out of band: %v", err)
	}
	d.teamCtrl.ReconcileOnce()

	resp = d.handleGet(mustMarshal(t, getParams{ResourceType: "budgets"}))
	if resp.Error != "" {
		t.Fatalf("get budgets: %s", resp.Error)
	}
	rows = nil
	if err := json.Unmarshal(resp.Result, &rows); err != nil {
		t.Fatalf("unmarshal rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d row(s), want 1: %+v", len(rows), rows)
	}
	if rows[0].State != admission.RowRefusing {
		t.Errorf("state = %q with a role held, want %q", rows[0].State, admission.RowRefusing)
	}
	if !strings.Contains(rows[0].Note, "max_sessions=3") {
		t.Errorf("note = %q, want the held role's arithmetic", rows[0].Note)
	}
}

// TestTokenBudgetWindowReachesTheLogRing covers the one observability
// affordance the max_tokens honesty caveat has: the meter is in-memory, so the
// window restarts with the daemon and with `marvel daemon reexec`, and the
// admin guide promises the daemon says so where `marvel daemon logs` can find
// it.
//
// Falsification: announced from the constructor, the line is written before
// cmd/marvel installs log.SetOutput(ring, --log-file), so it reaches bare
// stderr and neither surface the docs name. Announcing it from Start is what
// puts it in the ring.
func TestTokenBudgetWindowReachesTheLogRing(t *testing.T) {
	skipIfNoTmux(t)
	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	// A team the daemon already knows about at Start, which is the rehydrated
	// case. No roles, so the reconciler has nothing to spawn and the test
	// exercises the announcement alone.
	if err := d.store.CreateTeam(&api.Team{
		Name: "crew", Workspace: "tokenwindow",
		Budget: api.Budget{MaxTokens: 2_000_000},
	}); err != nil {
		t.Fatalf("create team: %v", err)
	}

	// The ordering under test: log output is installed AFTER construction,
	// exactly as cmd/marvel does it.
	prevFlags := log.Flags()
	log.SetOutput(d.LogBuffer())
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(prevFlags)
	})

	// os.TempDir rather than t.TempDir: a Unix socket path has a hard length
	// limit (about 104 bytes on darwin) and a per-test temp dir blows past it.
	sock := filepath.Join(os.TempDir(), "marvel-test-tokenwindow.sock")
	if err := d.Start(sock); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		d.Stop()
		_ = os.Remove(sock)
	})

	// Tail takes a positive count; 0 returns nothing.
	lines := d.LogBuffer().Tail(DefaultLogBufferLines)
	found := false
	for _, line := range lines {
		if strings.Contains(line, "declares budget.max_tokens=2000000") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("the token-budget window was not announced in the log ring; lines: %v", lines)
	}
}

// TestAdmissionSnapshotReportsUnobservedSessions covers the hole
// TeamTotals.Partial does not cover. Bind runs only inside attachInstance,
// which only Manager.Create calls, so a pane adopted from a prior daemon has
// no accountant state: it contributes nothing to the total AND does not set
// Partial. Comparing the store's live count against the meter's is what
// catches it.
func TestAdmissionSnapshotReportsUnobservedSessions(t *testing.T) {
	d := newHandlerDaemon(t)
	if resp := applyManifest(t, d, budgetedManifest); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	team, err := d.store.GetTeam("fanout/crew")
	if err != nil {
		t.Fatalf("get team: %v", err)
	}

	snap := d.AdmissionSnapshot(team)
	if snap.LiveSessions != 3 || snap.DeclaredSessions != 3 {
		t.Errorf("snapshot counts = live %d, declared %d; want 3 and 3", snap.LiveSessions, snap.DeclaredSessions)
	}
	if !snap.TokensMetered {
		t.Error("TokensMetered is false with an accountant wired; absence would read as zero spend")
	}
	// Interactive sleep sessions publish no stream, so the meter knows none
	// of them and the total is a floor rather than a small number.
	if !snap.TokensPartial {
		t.Error("three unobserved live sessions did not mark the total partial")
	}
}
