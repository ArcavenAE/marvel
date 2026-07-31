package main

import (
	"strings"
	"testing"
	"time"

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

func column(t *testing.T, table, name string) string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(table, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("table has no rows:\n%s", table)
	}
	header := strings.Fields(lines[0])
	row := strings.Fields(lines[1])
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

// CTX% had one producer, the heartbeat RPC, and no runtime adapter
// implements it. A session that has never sent one has no context
// reading, and "0%" would read as a fresh window rather than as silence.
func TestRenderSessionTableNoHeartbeat(t *testing.T) {
	table := renderSessionTable([]api.Session{{
		Name: "agent-0", Workspace: "ws", Team: "squad", Role: "worker",
		State: api.SessionRunning, PaneID: "%3", Runtime: api.Runtime{Name: "forestage"},
	}})
	if got := column(t, table, "CTX%"); got != "-" {
		t.Errorf("CTX%% = %q with no heartbeat, want %q", got, "-")
	}
}

func TestRenderSessionTableWithHeartbeat(t *testing.T) {
	table := renderSessionTable([]api.Session{{
		Name: "agent-0", Workspace: "ws", Team: "squad", Role: "worker",
		State: api.SessionRunning, PaneID: "%3", Runtime: api.Runtime{Name: "forestage"},
		ContextPercent: 0, LastHeartbeat: time.Now().UTC(),
	}})
	if got := column(t, table, "CTX%"); got != "0%" {
		t.Errorf("CTX%% = %q after a heartbeat reporting 0, want %q", got, "0%")
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
