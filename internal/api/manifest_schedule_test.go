package api

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// scheduleManifest is one team with one role in the given runtime mode,
// carrying the given role-level lines and schedule block. Replicas is 0 so
// nothing here would ever spawn. The required active_deadline and
// stale_after are added unless the schedule names them, which a case that
// tests their absence does in a TOML comment.
func scheduleManifest(mode, roleExtra, schedule string) string {
	if !strings.Contains(schedule, "active_deadline") {
		schedule += "\nactive_deadline = \"45m\""
	}
	if !strings.Contains(schedule, "stale_after") {
		schedule += "\nstale_after = \"30h\""
	}
	return fmt.Sprintf(`
[workspace]
name = "test"

[[team]]
name = "squad"

  [[team.role]]
  name = "board-refresh"
  replicas = 0
%s
    [team.role.runtime]
    command = "claude"
    mode = %q
    prompt = "/refresh"

    [team.role.schedule]
%s
`, roleExtra, mode, schedule)
}

// TestScheduleRefusals covers design section 2a tests 1 and 2 and the
// validation around them (marvel docs/design/scheduled-runs.md, S-1).
// Every case is refused at parse, before anything reaches the store.
func TestScheduleRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		mode      string
		roleExtra string
		schedule  string
		want      []string
		notWant   []string
	}{
		{
			name:     "timezone missing (test 1)",
			mode:     "headless",
			schedule: `cron = "17 6 * * *"`,
			want:     []string{"timezone is required"},
		},
		{
			name:     "cron missing",
			mode:     "headless",
			schedule: `timezone = "Etc/UTC"`,
			want:     []string{"cron is required"},
		},
		{
			name:     "a DST zone without dst_ack (test 2)",
			mode:     "headless",
			schedule: "cron = \"17 6 * * *\"\ntimezone = \"America/Chicago\"",
			want: []string{
				"America/Chicago observes daylight saving",
				"move by 1h against UTC",
				`timezone = "-06:00"`,
				"06:17 local in winter and 07:17 local in summer",
				`timezone = "Etc/UTC", cron = "17 12 * * *"`,
				"dst_ack = true",
			},
		},
		{
			name:     "the computed shift, not an hour",
			mode:     "headless",
			schedule: "cron = \"0 6 * * *\"\ntimezone = \"Australia/Lord_Howe\"",
			want:     []string{"move by 30m against UTC", `timezone = "+10:30"`},
		},
		{
			// tzdata models Dublin's winter as negative daylight saving, so
			// a check keyed to IsDST would name the summer offset as the
			// fixed one. Detection by offset change names the winter one.
			name:     "a zone with negative DST offers its winter offset",
			mode:     "headless",
			schedule: "cron = \"0 6 * * *\"\ntimezone = \"Europe/Dublin\"",
			want:     []string{"Europe/Dublin observes daylight saving", `timezone = "+00:00"`},
		},
		{
			name:     "southern hemisphere",
			mode:     "headless",
			schedule: "cron = \"0 6 * * *\"\ntimezone = \"Australia/Sydney\"",
			want:     []string{`timezone = "+10:00"`},
		},
		{
			// 20:00 in Chicago is the next UTC day, and the weekday field is
			// set, so no correct UTC cron exists as a one-line rewrite.
			name:     "no UTC cron when the day would shift",
			mode:     "headless",
			schedule: "cron = \"0 20 * * 1\"\ntimezone = \"America/Chicago\"",
			want:     []string{`timezone = "Etc/UTC"`, "convert the cron by hand"},
			notWant:  []string{`cron = "`},
		},
		{
			name:     "an interactive role",
			mode:     "interactive",
			schedule: "cron = \"17 6 * * *\"\ntimezone = \"Etc/UTC\"",
			want:     []string{"schedule is only valid on a headless role"},
		},
		{
			name:     "Local is a hidden default",
			mode:     "headless",
			schedule: "cron = \"17 6 * * *\"\ntimezone = \"Local\"",
			want:     []string{"timezone \"Local\""},
		},
		{
			name:     "an unknown zone",
			mode:     "headless",
			schedule: "cron = \"17 6 * * *\"\ntimezone = \"Mars/Olympus_Mons\"",
			want:     []string{"timezone \"Mars/Olympus_Mons\""},
		},
		{
			name:     "a minute out of range",
			mode:     "headless",
			schedule: "cron = \"61 6 * * *\"\ntimezone = \"Etc/UTC\"",
			want:     []string{"cron", "minute"},
		},
		{
			name:     "six fields",
			mode:     "headless",
			schedule: "cron = \"0 17 6 * * *\"\ntimezone = \"Etc/UTC\"",
			want:     []string{"cron", "five fields"},
		},
		{
			name:     "a descriptor",
			mode:     "headless",
			schedule: "cron = \"@daily\"\ntimezone = \"Etc/UTC\"",
			want:     []string{"cron", "five fields"},
		},
		{
			name:     "concurrency typo",
			mode:     "headless",
			schedule: "cron = \"17 6 * * *\"\ntimezone = \"Etc/UTC\"\nconcurrency = \"sometimes\"",
			want:     []string{"concurrency \"sometimes\""},
		},
		{
			name:     "on_failure typo",
			mode:     "headless",
			schedule: "cron = \"17 6 * * *\"\ntimezone = \"Etc/UTC\"\non_failure = \"retry\"",
			want:     []string{"on_failure \"retry\""},
		},
		{
			name:     "negative retries",
			mode:     "headless",
			schedule: "cron = \"17 6 * * *\"\ntimezone = \"Etc/UTC\"\nretries = -1",
			want:     []string{"retries must be >= 0"},
		},
		{
			name:     "a duration that does not parse",
			mode:     "headless",
			schedule: "cron = \"17 6 * * *\"\ntimezone = \"Etc/UTC\"\nstarting_deadline = \"soon\"",
			want:     []string{"starting_deadline"},
		},
		{
			name:     "a zero duration",
			mode:     "headless",
			schedule: "cron = \"17 6 * * *\"\ntimezone = \"Etc/UTC\"\nactive_deadline = \"0s\"",
			want:     []string{"active_deadline must be > 0"},
		},
		{
			name:     "active_deadline missing",
			mode:     "headless",
			schedule: "cron = \"17 6 * * *\"\ntimezone = \"Etc/UTC\"\n# no active_deadline",
			want:     []string{"active_deadline is required"},
		},
		{
			name:     "stale_after missing",
			mode:     "headless",
			schedule: "cron = \"17 6 * * *\"\ntimezone = \"Etc/UTC\"\n# no stale_after",
			want:     []string{"stale_after is required"},
		},
		{
			name:     "zero history",
			mode:     "headless",
			schedule: "cron = \"17 6 * * *\"\ntimezone = \"Etc/UTC\"\nhistory = { succeeded = 3, failed = 0 }",
			want:     []string{"history.failed must be > 0"},
		},
		{
			name:     "negative history",
			mode:     "headless",
			schedule: "cron = \"17 6 * * *\"\ntimezone = \"Etc/UTC\"\nhistory = { succeeded = -1, failed = 3 }",
			want:     []string{"history.succeeded must be > 0"},
		},
		{
			// The whole status is one stored value rewritten on every run,
			// so "bounded" needs a ceiling as well as a floor.
			name:     "history over the cap",
			mode:     "headless",
			schedule: "cron = \"17 6 * * *\"\ntimezone = \"Etc/UTC\"\nhistory = { succeeded = 3, failed = 51 }",
			want:     []string{"history keeps at most 50 runs of each outcome"},
		},
		{
			// ADR-010 Amendment 1: the restart policy does not apply to a
			// scheduled role, so setting it would silently do nothing.
			name:      "restart_policy on a scheduled role",
			mode:      "headless",
			roleExtra: `  restart_policy = "always"`,
			schedule:  "cron = \"17 6 * * *\"\ntimezone = \"Etc/UTC\"",
			want:      []string{"restart_policy does not apply to a scheduled role", "retries", "on_failure"},
		},
		{
			name:      "max_restarts on a scheduled role",
			mode:      "headless",
			roleExtra: `  max_restarts = 3`,
			schedule:  "cron = \"17 6 * * *\"\ntimezone = \"Etc/UTC\"",
			want:      []string{"max_restarts does not apply to a scheduled role"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseManifestBytes([]byte(scheduleManifest(c.mode, c.roleExtra, c.schedule)))
			if err == nil {
				t.Fatalf("parse accepted a schedule it must refuse")
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err, w)
				}
			}
			for _, nw := range c.notWant {
				if strings.Contains(err.Error(), nw) {
					t.Errorf("error %q contains %q", err, nw)
				}
			}
			if !strings.Contains(err.Error(), "squad") || !strings.Contains(err.Error(), "board-refresh") {
				t.Errorf("error %q does not name the team and role", err)
			}
		})
	}
}

// TestScheduleAccepts covers design section 2a tests 3 and 4 at parse: fixed
// zones need no acknowledgement, and a DST zone with dst_ack is accepted.
func TestScheduleAccepts(t *testing.T) {
	t.Parallel()
	for _, schedule := range []string{
		"cron = \"17 6 * * *\"\ntimezone = \"Etc/UTC\"",
		"cron = \"17 6 * * *\"\ntimezone = \"+00:00\"",
		"cron = \"17 6 * * *\"\ntimezone = \"-06:00\"",
		"cron = \"*/15 9-17 * * 1-5\"\ntimezone = \"+05:30\"",
		"cron = \"17 6 * * *\"\ntimezone = \"America/Chicago\"\ndst_ack = true",
		"cron = \"0 0 1,15 * 0,7\"\ntimezone = \"Etc/UTC\"\nconcurrency = \"allow\"\non_failure = \"freeze\"\nretries = 2\nstarting_deadline = \"2h\"\nactive_deadline = \"45m\"\njitter = \"5m\"\nstale_after = \"30h\"\nhistory = { succeeded = 3, failed = 3 }\nsuspend = true",
	} {
		if _, err := ParseManifestBytes([]byte(scheduleManifest("headless", "", schedule))); err != nil {
			t.Errorf("schedule %q refused: %v", schedule, err)
		}
	}
}

// TestScheduleCarriedOntoRole: a valid block lands on Role.Schedule after
// apply, with the ruled defaults (concurrency forbid, on_failure wait).
func TestScheduleCarriedOntoRole(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(scheduleManifest("headless", "",
		"cron = \"17 6 * * *\"\ntimezone = \"Etc/UTC\"\nstarting_deadline = \"2h\"\nactive_deadline = \"45m\"\njitter = \"5m\"\nstale_after = \"30h\"\nretries = 1\nhistory = { succeeded = 3, failed = 2 }")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	team, _ := store.GetTeam("test/squad")
	s := team.Roles[0].Schedule
	if s == nil {
		t.Fatal("expected a schedule policy on the role")
	}
	if s.Cron != "17 6 * * *" || s.Timezone != "Etc/UTC" {
		t.Errorf("cron, timezone = %q, %q", s.Cron, s.Timezone)
	}
	if s.Concurrency != ScheduleConcurrencyForbid || s.OnFailure != ScheduleOnFailureWait {
		t.Errorf("defaults: concurrency %q, on_failure %q; want forbid, wait", s.Concurrency, s.OnFailure)
	}
	if s.StartingDeadline != 2*time.Hour || s.ActiveDeadline != 45*time.Minute || s.Jitter != 5*time.Minute || s.StaleAfter != 30*time.Hour {
		t.Errorf("durations = %v %v %v %v", s.StartingDeadline, s.ActiveDeadline, s.Jitter, s.StaleAfter)
	}
	if s.Retries != 1 || s.History == nil || s.History.Succeeded != 3 || s.History.Failed != 2 {
		t.Errorf("retries %d, history %+v", s.Retries, s.History)
	}
}

// TestScheduleGapDefaults: the architect's ruling on what the design left
// open. Unset history keeps 3 of each outcome; unset jitter and
// starting_deadline stay zero (no delay; no catch-up, which S-3 enforces).
func TestScheduleGapDefaults(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(scheduleManifest("headless", "", "cron = \"17 6 * * *\"\ntimezone = \"Etc/UTC\"")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	team, _ := store.GetTeam("test/squad")
	s := team.Roles[0].Schedule
	if s.History == nil || s.History.Succeeded != 3 || s.History.Failed != 3 {
		t.Errorf("history = %+v, want succeeded 3, failed 3", s.History)
	}
	if s.Jitter != 0 || s.StartingDeadline != 0 {
		t.Errorf("jitter %v, starting_deadline %v; want both unset", s.Jitter, s.StartingDeadline)
	}
	if s.ActiveDeadline != 45*time.Minute || s.StaleAfter != 30*time.Hour {
		t.Errorf("active_deadline %v, stale_after %v", s.ActiveDeadline, s.StaleAfter)
	}
}

func TestScheduleAbsentIsNil(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(validManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, team := range store.ListTeams() {
		for _, r := range team.Roles {
			if r.Schedule != nil {
				t.Errorf("role %s/%s has a schedule it never declared", team.Name, r.Name)
			}
		}
	}
}

func TestScheduleYAML(t *testing.T) {
	t.Parallel()
	_, err := ParseManifestBytes([]byte(`
workspace:
  name: test
teams:
  - name: squad
    roles:
      - name: board-refresh
        replicas: 0
        runtime:
          command: claude
          mode: headless
        schedule:
          cron: "17 6 * * *"
`))
	if err == nil || !strings.Contains(err.Error(), "timezone is required") {
		t.Fatalf("YAML schedule without timezone: err = %v, want timezone is required", err)
	}
}

// TestDSTAcknowledgements covers the apply half of design 2a test 3: a DST
// zone with dst_ack is listed for schedule.dst-acknowledged; fixed zones,
// and a fixed zone that carries a needless ack, are not.
func TestDSTAcknowledgements(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		schedule string
		want     int
	}{
		{"cron = \"17 6 * * *\"\ntimezone = \"America/Chicago\"\ndst_ack = true", 1},
		{"cron = \"17 6 * * *\"\ntimezone = \"Etc/UTC\"\ndst_ack = true", 0},
		{"cron = \"17 6 * * *\"\ntimezone = \"-06:00\"", 0},
	} {
		m, err := ParseManifestBytes([]byte(scheduleManifest("headless", "", c.schedule)))
		if err != nil {
			t.Fatalf("parse %q: %v", c.schedule, err)
		}
		got := m.DSTAcknowledgements(time.Now().UTC())
		if len(got) != c.want {
			t.Errorf("%q: %d acknowledgements, want %d", c.schedule, len(got), c.want)
			continue
		}
		if c.want == 1 && (got[0].Team != "squad" || got[0].Role != "board-refresh" || got[0].Timezone != "America/Chicago") {
			t.Errorf("acknowledgement = %+v", got[0])
		}
	}
}

func TestParseCronFields(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		expr string
		ok   bool
	}{
		{"* * * * *", true},
		{"*/5 0-23/2 1,15 1-12 0-7", true},
		{"0 0 29 2 *", true},
		{"60 * * * *", false},
		{"* 24 * * *", false},
		{"* * 0 * *", false},
		{"* * * 13 *", false},
		{"* * * * 8", false},
		{"5-1 * * * *", false},
		{"*/0 * * * *", false},
		{"* * * JAN *", false},
		{"1,,2 * * * *", false},
		// A step on a single number has two readings in the wild (Vixie
		// reads 5/15 as 5-59/15); refused until S-3 picks one.
		{"5/15 * * * *", false},
		{"5-59/15 * * * *", true},
	} {
		_, err := parseCron(c.expr)
		if (err == nil) != c.ok {
			t.Errorf("parseCron(%q) err = %v, want ok=%v", c.expr, err, c.ok)
		}
	}
}

// TestScheduleSnapshotIsolated: a read returns a copy, so editing the
// returned schedule does not reach the store (go.md rule 12).
func TestScheduleSnapshotIsolated(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(scheduleManifest("headless", "",
		"cron = \"17 6 * * *\"\ntimezone = \"Etc/UTC\"\nhistory = { succeeded = 3, failed = 3 }")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	first, _ := store.GetTeam("test/squad")
	first.Roles[0].Schedule.Cron = "0 0 * * *"
	first.Roles[0].Schedule.History.Failed = 99
	again, _ := store.GetTeam("test/squad")
	if again.Roles[0].Schedule.Cron != "17 6 * * *" || again.Roles[0].Schedule.History.Failed != 3 {
		t.Fatalf("a snapshot edit reached the store: %+v", again.Roles[0].Schedule)
	}
}

// TestScheduleAdvisoryForNeedlessAck: dst_ack on a zone that never changes
// offset applies, and the operator is told the ack does nothing.
func TestScheduleAdvisoryForNeedlessAck(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		schedule string
		want     int
	}{
		{"cron = \"17 6 * * *\"\ntimezone = \"Etc/UTC\"\ndst_ack = true", 1},
		{"cron = \"17 6 * * *\"\ntimezone = \"America/Chicago\"\ndst_ack = true", 0},
		{"cron = \"17 6 * * *\"\ntimezone = \"-06:00\"", 0},
	} {
		m, err := ParseManifestBytes([]byte(scheduleManifest("headless", "", c.schedule)))
		if err != nil {
			t.Fatalf("parse %q: %v", c.schedule, err)
		}
		got := m.ScheduleAdvisories(time.Now().UTC())
		if len(got) != c.want {
			t.Errorf("%q: %d advisories %v, want %d", c.schedule, len(got), got, c.want)
		}
		if c.want == 1 && !strings.Contains(got[0], "does not observe daylight saving") {
			t.Errorf("advisory %q does not say why", got[0])
		}
	}
	_, err := parseCron("5/15 * * * *")
	if err == nil || !strings.Contains(err.Error(), "5-59/15") {
		t.Errorf("step-base refusal %v does not offer 5-59/15", err)
	}
}
