package api

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// cadenceScanBound is a guard on parse and apply of the most frequent cadence V1
// allows. Measured under the run conditions: 648d7b7 did not finish inside the
// default 10m go test timeout, and the one-pass scan takes about 0.6s, so the
// bound leaves room for a slow or race-instrumented run and still fails the
// per-slot reload by a wide margin.
const cadenceScanBound = 10 * time.Second

// docs/design/declared-cadence.md V1 (ticket T1): a role carries cadence
// entries, set only by applying a manifest. Apply refuses a cadence unless
// 1m <= starting_deadline <= grace < period, where period is the shortest gap
// between two consecutive slots over the next 366 days in the declared zone.

func cadenceManifest(role string, cadences ...string) string {
	return `
[workspace]
name = "test"

[[team]]
name = "squad"

  [[team.role]]
  name = "envoy"
  replicas = 1
` + role + `
    [team.role.runtime]
    command = "claude"
` + strings.Join(cadences, "\n")
}

// cadenceTable renders one [[team.role.cadence]] table. Fields are name=value
// lines; the defaults are a valid two-hourly sweep in UTC.
func cadenceTable(over map[string]string) string {
	f := map[string]string{
		"name":              `"sweep"`,
		"schedule":          `"0 */2 * * *"`,
		"timezone":          `"Etc/UTC"`,
		"prompt":            `"sweep the board"`,
		"starting_deadline": `"5m"`,
		"grace":             `"30m"`,
	}
	for k, v := range over {
		if v == "" {
			delete(f, k)
			continue
		}
		f[k] = v
	}
	var b strings.Builder
	b.WriteString("\n    [[team.role.cadence]]\n")
	for _, k := range []string{"name", "schedule", "timezone", "prompt", "starting_deadline", "grace"} {
		if v, ok := f[k]; ok {
			b.WriteString("    " + k + " = " + v + "\n")
		}
	}
	return b.String()
}

func TestCadenceIsCarriedOnTheAppliedRole(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(cadenceManifest("", cadenceTable(nil))))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	team, err := store.GetTeam("test/squad")
	if err != nil {
		t.Fatal(err)
	}
	want := []Cadence{{
		Name: "sweep", Schedule: "0 */2 * * *", Timezone: "Etc/UTC", Prompt: "sweep the board",
		StartingDeadline: 5 * time.Minute, Grace: 30 * time.Minute,
	}}
	if got := team.Roles[0].Cadence; !reflect.DeepEqual(got, want) {
		t.Fatalf("Role.Cadence = %+v, want %+v", got, want)
	}
	// A read is a snapshot: changing it does not change the stored role.
	team.Roles[0].Cadence[0].Prompt = "changed"
	again, err := store.GetTeam("test/squad")
	if err != nil {
		t.Fatal(err)
	}
	if again.Roles[0].Cadence[0].Prompt != "sweep the board" {
		t.Errorf("a read leaked the stored cadence: %q", again.Roles[0].Cadence[0].Prompt)
	}
}

func TestCadenceYAMLKeyIsCadences(t *testing.T) {
	t.Parallel()
	y := `
workspace:
  name: test
teams:
  - name: squad
    roles:
      - name: envoy
        replicas: 1
        runtime:
          command: claude
        cadences:
          - name: sweep
            schedule: "0 */2 * * *"
            timezone: Etc/UTC
            prompt: sweep the board
            starting_deadline: 5m
            grace: 30m
`
	m, err := ParseManifestBytes([]byte(y))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	team, err := store.GetTeam("test/squad")
	if err != nil {
		t.Fatal(err)
	}
	if len(team.Roles[0].Cadence) != 1 || team.Roles[0].Cadence[0].Grace != 30*time.Minute {
		t.Errorf("Role.Cadence = %+v", team.Roles[0].Cadence)
	}
}

func TestCadenceRefusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		role string
		over map[string]string
		more string // a second table
		want string
	}{
		{name: "no timezone", over: map[string]string{"timezone": ""}, want: "timezone is required"},
		{name: "an unknown zone", over: map[string]string{"timezone": `"Mars/Olympus"`}, want: "timezone"},
		{name: "no name", over: map[string]string{"name": ""}, want: "name is required"},
		{name: "no prompt", over: map[string]string{"prompt": ""}, want: "prompt is required"},
		{name: "a bad cron", over: map[string]string{"schedule": `"every day"`}, want: "five fields"},
		{name: "no schedule", over: map[string]string{"schedule": ""}, want: "schedule is required"},
		{name: "no starting_deadline", over: map[string]string{"starting_deadline": ""}, want: "starting_deadline is required"},
		{name: "no grace", over: map[string]string{"grace": ""}, want: "grace is required"},
		{name: "starting_deadline below one minute", over: map[string]string{"starting_deadline": `"30s"`}, want: "at least 1m"},
		{name: "starting_deadline above grace", over: map[string]string{"starting_deadline": `"40m"`}, want: "starting_deadline"},
		{name: "grace equal to the period", over: map[string]string{"grace": `"2h"`}, want: "grace"},
		{name: "grace above the period", over: map[string]string{"grace": `"3h"`}, want: "grace"},
		{
			// 09:00 and 10:00 are an hour apart and the next 09:00 is 23 hours on;
			// the period is the shortest gap, not the first or the longest.
			name: "grace above the shortest gap of an uneven schedule",
			over: map[string]string{"schedule": `"0 9,10 * * *"`, "grace": `"90m"`},
			want: "grace",
		},
		{name: "fewer than two slots in 366 days", over: map[string]string{"schedule": `"0 0 29 2 *"`}, want: "two slots"},
		{name: "a duplicate name in one role", more: cadenceTable(nil), want: "duplicate"},
		{name: "a headless role", role: "    # headless\n", over: nil, want: "interactive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			role := tt.role
			text := cadenceManifest(role, cadenceTable(tt.over), tt.more)
			if tt.name == "a headless role" {
				text = strings.Replace(text, `command = "claude"`, "command = \"claude\"\n    mode = \"headless\"\n    prompt = \"go\"", 1)
			}
			_, err := ParseManifestBytes([]byte(text))
			if err == nil {
				t.Fatalf("manifest accepted, want a refusal mentioning %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestCadenceAcceptsTheEdges(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		over map[string]string
	}{
		{name: "starting_deadline of exactly one minute", over: map[string]string{"starting_deadline": `"1m"`}},
		{name: "starting_deadline equal to grace", over: map[string]string{"starting_deadline": `"30m"`}},
		{name: "grace just under the period", over: map[string]string{"grace": `"119m"`}},
		{name: "grace under the shortest gap of an uneven schedule", over: map[string]string{"schedule": `"0 9,10 * * *"`, "grace": `"30m"`}},
		{name: "a zone that observes daylight saving", over: map[string]string{"timezone": `"America/Chicago"`, "grace": `"30m"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseManifestBytes([]byte(cadenceManifest("", cadenceTable(tt.over)))); err != nil {
				t.Errorf("manifest refused: %v", err)
			}
		})
	}
}

// The period scan is the cost of an apply. A schedule as frequent as V1 allows
// (every two minutes, with starting_deadline and grace at their 1m floor) holds
// its grace and so is walked for the whole year. Parse and apply together must
// stay inside a bound that a per-slot zone and cron reload would not.
func TestCadencePeriodScanIsBounded(t *testing.T) {
	// Not parallel: the other tests would be sharing the CPU the bound is read against.
	text := cadenceManifest("", cadenceTable(map[string]string{
		"schedule": `"*/2 * * * *"`, "starting_deadline": `"1m"`, "grace": `"1m"`,
	}))
	start := time.Now()
	m, err := ParseManifestBytes([]byte(text))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.Apply(NewStore()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	took := time.Since(start)
	t.Logf("parse and apply of a */2 cadence took %s", took)
	if took > cadenceScanBound {
		t.Errorf("parse and apply took %s, want under %s", took, cadenceScanBound)
	}
}
