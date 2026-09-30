package api

import (
	"fmt"
	"strings"
	"testing"
)

// scheduleManifest is one team with one role in the given runtime mode,
// carrying the given role-level lines and schedule block. Replicas is 0 so
// nothing here would ever spawn.
func scheduleManifest(mode, roleExtra, schedule string) string {
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
			name:     "negative history",
			mode:     "headless",
			schedule: "cron = \"17 6 * * *\"\ntimezone = \"Etc/UTC\"\nhistory = { succeeded = -1, failed = 3 }",
			want:     []string{"history.succeeded must be >= 0"},
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
