package main

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/admission"
	"github.com/arcavenae/marvel/internal/api"
)

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0B"},
		{512, "512B"},
		{1024, "1.0K"},
		{1536, "1.5K"},
		{100 * 1024, "100K"},
		{4 * 1024 * 1024, "4.0M"},
		{512 * 1024 * 1024, "512M"},
		{3 * 1024 * 1024 * 1024, "3.0G"},
	}
	for _, c := range cases {
		if got := formatBytes(c.in); got != c.want {
			t.Errorf("formatBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// splitColumns splits a tabwriter-rendered line on runs of two or more
// spaces, so multi-word headers ("AGENT NAME") stay one column.
func splitColumns(line string) []string {
	var out []string
	for _, f := range regexp.MustCompile(`\s{2,}`).Split(strings.TrimSpace(line), -1) {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func column(t *testing.T, table, name string) string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(table, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("table has no rows:\n%s", table)
	}
	header := splitColumns(lines[0])
	row := splitColumns(lines[1])
	if len(header) != len(row) {
		t.Fatalf("header has %d columns, row has %d:\n%s", len(header), len(row), table)
	}
	for i, h := range header {
		if h == name {
			return row[i]
		}
	}
	t.Fatalf("no %s column in:\n%s", name, table)
	return ""
}

// A session marvel has never measured has no context reading at all, and
// "0%" would read as a fresh window rather than as silence. That is the
// interactive launch, the pane adopted from a prior daemon, and the
// harness with no stream.
func TestRenderSessionTableNeverMeasured(t *testing.T) {
	table := renderSessionTable([]api.Session{{
		Name: "agent-0", Workspace: "ws", Team: "squad", Role: "worker",
		State: api.SessionRunning, PaneID: "%3", Runtime: api.Runtime{Name: "forestage"},
	}})
	if got := column(t, table, "CTX%"); got != "-" {
		t.Errorf("CTX%% = %q with no reading, want %q", got, "-")
	}
}

// A measured session reporting 0% must be distinguishable from an
// unmeasured one.
func TestRenderSessionTableMeasuredIdle(t *testing.T) {
	table := renderSessionTable([]api.Session{{
		Name: "agent-0", Workspace: "ws", Team: "squad", Role: "worker",
		State: api.SessionRunning, PaneID: "%3", Runtime: api.Runtime{Name: "forestage"},
		SessionContext: api.SessionContext{
			ContextPercent: 0, ContextLimit: 200_000, ContextAt: time.Now().UTC(),
		},
	}})
	if got := column(t, table, "CTX%"); got != "0%" {
		t.Errorf("CTX%% = %q for a measured empty window, want %q", got, "0%")
	}
}

// The activity-staleness advisory (aae-orc-9box) rides in the HEALTH column
// as a parenthetical: HEALTH is liveness, so a stalled session still reads
// healthy — the suffix is the "marvel has not seen it work" signal liveness
// cannot carry.
func TestRenderSessionTableStalledAdvisory(t *testing.T) {
	table := renderSessionTable([]api.Session{{
		Name: "agent-0", Workspace: "ws", Team: "squad", Role: "worker",
		State: api.SessionRunning, PaneID: "%3", Runtime: api.Runtime{Name: "claude"},
		HealthState: api.HealthHealthy, ActivityState: api.ActivityStalled,
	}})
	if got := column(t, table, "HEALTH"); got != "healthy (stalled)" {
		t.Errorf("HEALTH = %q for a stalled running session, want %q", got, "healthy (stalled)")
	}
}

// Active and Unknown add nothing — the advisory is a stalled-only annotation.
func TestRenderSessionTableActiveNoAdvisory(t *testing.T) {
	table := renderSessionTable([]api.Session{{
		Name: "agent-0", Workspace: "ws", Team: "squad", Role: "worker",
		State: api.SessionRunning, PaneID: "%3", Runtime: api.Runtime{Name: "claude"},
		HealthState: api.HealthHealthy, ActivityState: api.ActivityActive,
	}})
	if got := column(t, table, "HEALTH"); got != "healthy" {
		t.Errorf("HEALTH = %q for an active session, want plain %q", got, "healthy")
	}
}

// A terminated session's last advisory is not news, so the annotation is
// gated on running state: a stopped-but-stalled row reads plainly.
func TestRenderSessionTableStalledButNotRunning(t *testing.T) {
	table := renderSessionTable([]api.Session{{
		Name: "agent-0", Workspace: "ws", Team: "squad", Role: "worker",
		State: api.SessionFailed, PaneID: "%3", Runtime: api.Runtime{Name: "claude"},
		HealthState: api.HealthHealthy, ActivityState: api.ActivityStalled,
	}})
	if got := column(t, table, "HEALTH"); got != "healthy" {
		t.Errorf("HEALTH = %q for a non-running stalled session, want plain %q", got, "healthy")
	}
}

func TestRenderSessionTableMeasured(t *testing.T) {
	table := renderSessionTable([]api.Session{{
		Name: "agent-0", Workspace: "ws", Team: "squad", Role: "worker",
		State: api.SessionRunning, PaneID: "%3", Runtime: api.Runtime{Name: "claude"},
		SessionContext: api.SessionContext{
			ContextTokens: 34136, ContextLimit: 1_000_000, ContextPercent: 3.4136,
			ContextAt: time.Now().UTC(),
		},
	}})
	if got := column(t, table, "CTX%"); got != "3%" {
		t.Errorf("CTX%% = %q, want %q", got, "3%")
	}
}

// The third state: tokens are real, the window is not. A percentage here
// would be a fiction against a guessed denominator, so the cell says the
// reading exists but cannot be scaled. See orc finding-055.
func TestRenderSessionTableMeasuredWithoutLimit(t *testing.T) {
	table := renderSessionTable([]api.Session{{
		Name: "agent-0", Workspace: "ws", Team: "squad", Role: "worker",
		State: api.SessionRunning, PaneID: "%3", Runtime: api.Runtime{Name: "codex"},
		SessionContext: api.SessionContext{
			ContextTokens: 13992, ContextRequests: 1, ContextLimit: 0,
			ContextAt: time.Now().UTC(),
		},
	}})
	if got := column(t, table, "CTX%"); got != "?" {
		t.Errorf("CTX%% = %q with tokens but no window, want %q", got, "?")
	}
}

// The cooperative heartbeat RPC is still a producer, and it was the only
// one before the accountant existed, so the simulator must keep lighting
// the column.
//
// Driven through the real store rather than a hand-built struct: the
// heartbeat path reports a percentage with no token count and no window,
// a shape no hand-written fixture would think to reproduce, and a
// renderer keyed on the window alone blanks it. That is exactly how this
// regressed once.
func TestRenderSessionTableWithHeartbeat(t *testing.T) {
	store := api.NewStore()
	if err := store.CreateWorkspace(&api.Workspace{Name: "ws"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := store.CreateSession(&api.Session{
		Name: "agent-0", Workspace: "ws", Team: "squad", Role: "worker",
		Runtime: api.Runtime{Name: "simulator", Command: "simulator"},
		State:   api.SessionRunning, PaneID: "%3",
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := store.UpdateSessionHeartbeat(api.HeartbeatRequest{SessionKey: "ws/agent-0", ContextPercent: 55.4}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	sess, err := store.GetSession("ws/agent-0")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess.ContextLimit != 0 || sess.ContextTokens != 0 {
		t.Fatalf("the heartbeat path produced a window or a token count: %+v", sess.SessionContext)
	}
	table := renderSessionTable([]api.Session{sess})
	if got := column(t, table, "CTX%"); got != "55%" {
		t.Errorf("CTX%% = %q after a heartbeat reporting 55.4, want %q", got, "55%")
	}
}

func TestRenderSessionTableUnsampled(t *testing.T) {
	table := renderSessionTable([]api.Session{{
		Name: "agent-0", Workspace: "ws", Team: "squad", Role: "worker",
		State: api.SessionRunning, PaneID: "%3", Runtime: api.Runtime{Name: "forestage"},
	}})
	if got := column(t, table, "CPU%"); got != "-" {
		t.Errorf("CPU%% = %q before the first sampler pass, want %q", got, "-")
	}
	if got := column(t, table, "RSS"); got != "-" {
		t.Errorf("RSS = %q before the first sampler pass, want %q", got, "-")
	}
}

func TestRenderSessionTableSampled(t *testing.T) {
	table := renderSessionTable([]api.Session{{
		Name: "agent-0", Workspace: "ws", Team: "squad", Role: "worker",
		State: api.SessionRunning, PaneID: "%3", Runtime: api.Runtime{Name: "forestage"},
		SessionMetrics: api.SessionMetrics{
			CPUPercent: 12.75,
			RSSBytes:   512 * 1024 * 1024,
			MetricsAt:  time.Now().UTC(),
		},
	}})
	if got := column(t, table, "CPU%"); got != "12.8" {
		t.Errorf("CPU%% = %q, want %q", got, "12.8")
	}
	if got := column(t, table, "RSS"); got != "512M" {
		t.Errorf("RSS = %q, want %q", got, "512M")
	}
}

// An idle sampled session must be distinguishable from an unsampled one.
func TestRenderSessionTableSampledIdle(t *testing.T) {
	table := renderSessionTable([]api.Session{{
		Name: "agent-0", Workspace: "ws", Team: "squad", Role: "worker",
		State: api.SessionRunning, PaneID: "%3", Runtime: api.Runtime{Name: "forestage"},
		SessionMetrics: api.SessionMetrics{MetricsAt: time.Now().UTC()},
	}})
	if got := column(t, table, "CPU%"); got != "0.0" {
		t.Errorf("CPU%% = %q for a sampled idle session, want %q", got, "0.0")
	}
	if got := column(t, table, "RSS"); got != "0B" {
		t.Errorf("RSS = %q for a sampled idle session, want %q", got, "0B")
	}
}

func TestSortSessionsByCPUAndRSS(t *testing.T) {
	sessions := []api.Session{
		{Name: "a", SessionMetrics: api.SessionMetrics{CPUPercent: 1, RSSBytes: 300}},
		{Name: "b", SessionMetrics: api.SessionMetrics{CPUPercent: 50, RSSBytes: 100}},
		{Name: "c", SessionMetrics: api.SessionMetrics{CPUPercent: 10, RSSBytes: 200}},
	}

	sortSessions(sessions, &watchSort{column: "cpu", desc: true})
	if sessions[0].Name != "b" {
		t.Errorf("cpu desc put %q first, want b", sessions[0].Name)
	}

	sortSessions(sessions, &watchSort{column: "rss", desc: true})
	if sessions[0].Name != "a" {
		t.Errorf("rss desc put %q first, want a", sessions[0].Name)
	}
}

// budgetRow returns one rendered row keyed by column name, so a test asserts
// on cells rather than on whitespace. Every cell in this table is non-empty
// (absence renders as a dash), so splitting on fields is unambiguous.
func budgetRow(t *testing.T, table, dimension string) map[string]string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(table, "\n"), "\n")
	header := strings.Fields(lines[0])
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < len(header) || fields[2] != dimension {
			continue
		}
		out := make(map[string]string, len(header))
		for i, h := range header {
			if h == "NOTE" {
				// The note is prose and holds spaces, so it takes the rest.
				out[h] = strings.Join(fields[i:], " ")
				break
			}
			out[h] = fields[i]
		}
		return out
	}
	t.Fatalf("no %s row in:\n%s", dimension, table)
	return nil
}

// TestRenderBudgetTable covers `marvel get budgets`, the surface that answers
// which dimension tripped and by how much. Its absence handling matters for
// the same reason CTX%'s does: a token figure nothing has measured must not
// render as headroom the operator does not have.
func TestRenderBudgetTable(t *testing.T) {
	rows := []admission.Row{
		{
			Workspace: "fanout", Team: "crew", Dimension: api.DimMaxTokens,
			Limit: 2000000, Observed: 412118, Headroom: 1587882,
			State: admission.RowOK, Window: time.Now().UTC().Add(-14 * time.Minute),
			Note: "partial: some sessions unobserved, so this is a floor",
		},
		{
			Workspace: "fanout", Team: "crew", Dimension: api.DimMaxSessions,
			Limit: 6, Observed: 6, Headroom: 0, State: admission.RowAtCeiling,
		},
	}
	table := renderBudgetTable(rows)

	// Sorted by dimension within a team, so max_sessions comes first.
	lines := strings.Split(strings.TrimRight(table, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d line(s), want a header and two rows:\n%s", len(lines), table)
	}
	if !strings.Contains(lines[1], string(api.DimMaxSessions)) {
		t.Errorf("first row = %q, want max_sessions (rows sort by dimension)", lines[1])
	}

	sessions := budgetRow(t, table, string(api.DimMaxSessions))
	// No headroom is not a refusal: a team whose declared replicas equal its
	// ceiling sits here permanently and refuses nothing.
	if sessions["STATE"] != admission.RowAtCeiling {
		t.Errorf("STATE = %q, want %q with no headroom and nothing refused", sessions["STATE"], admission.RowAtCeiling)
	}
	if sessions["HEADROOM"] != "0" {
		t.Errorf("HEADROOM = %q, want 0", sessions["HEADROOM"])
	}
	// A count dimension accumulates over no window, so it must render
	// absence rather than invent one.
	if sessions["WINDOW"] != "-" {
		t.Errorf("WINDOW = %q for a count dimension, want a dash", sessions["WINDOW"])
	}
	if sessions["NOTE"] != "-" {
		t.Errorf("NOTE = %q with nothing to say, want a dash", sessions["NOTE"])
	}

	tokens := budgetRow(t, table, string(api.DimMaxTokens))
	if tokens["HEADROOM"] != "1587882" {
		t.Errorf("HEADROOM = %q, want 1587882", tokens["HEADROOM"])
	}
	if tokens["WINDOW"] == "-" {
		t.Errorf("WINDOW = %q for a cumulative dimension, want the elapsed span", tokens["WINDOW"])
	}
	if !strings.Contains(tokens["NOTE"], "partial") {
		t.Errorf("NOTE = %q, want the partial-total notice", tokens["NOTE"])
	}
}

// TestFormatWindow: a dimension that accumulates over no window renders
// absence, never a zero duration that would read as "just reset".
func TestFormatWindow(t *testing.T) {
	if got := formatWindow(time.Time{}); got != "-" {
		t.Errorf("formatWindow(zero) = %q, want %q", got, "-")
	}
	if got := formatWindow(time.Now().UTC().Add(-90 * time.Second)); got == "-" {
		t.Errorf("formatWindow(90s ago) = %q, want an elapsed duration", got)
	}
}

// TestRenderSessionTableMarksTerminalRoles is the rendering half of
// aae-orc-kj5bq. The projection annotates a saturated or frozen role's row
// with a Reason, but until the table shows it the information dies one layer
// short of the operator — which is the same defect the ticket describes,
// moved rather than fixed. The STATE cell carries a short suffix, the same
// idiom HEALTH already uses for "(stalled)".
func TestRenderSessionTableMarksTerminalRoles(t *testing.T) {
	tests := []struct {
		name    string
		session api.Session
		want    string
		notWant string
	}{
		{
			name:    "saturated role is marked",
			session: api.Session{Name: "a", State: api.SessionFailed, Reason: "saturated: max_restarts=3 reached after 3 restart(s), no replacement will be spawned"},
			want:    "failed (saturated)",
		},
		{
			name:    "frozen role is marked",
			session: api.Session{Name: "b", State: api.SessionFailed, Reason: "frozen: restart_policy=never, no replacement will be spawned"},
			want:    "failed (frozen)",
		},
		{
			// The ordinary case, and the one that must not regress: a plain
			// failure is being replaced by the reconciler and gets no suffix.
			name:    "plain failure is unmarked",
			session: api.Session{Name: "c", State: api.SessionFailed},
			want:    "failed",
			notWant: "failed (",
		},
		{
			// A live row must never be suffixed even if it somehow carries
			// Reason — terminal means terminal.
			name:    "running row is never marked",
			session: api.Session{Name: "d", State: api.SessionRunning, Reason: "saturated: ignore me"},
			want:    "running",
			notWant: "(saturated)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderSessionTable([]api.Session{tt.session})
			if !strings.Contains(got, tt.want) {
				t.Errorf("table missing %q:\n%s", tt.want, got)
			}
			if tt.notWant != "" && strings.Contains(got, tt.notWant) {
				t.Errorf("table unexpectedly contains %q:\n%s", tt.notWant, got)
			}
		})
	}
}
