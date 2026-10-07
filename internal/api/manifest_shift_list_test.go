package api

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Tests for the shift trigger list (marvel#437, docs/design/shift-trigger-list.md
// section 6, items 1 to 3). Each manifest is written in both TOML and YAML,
// because the two decoders drop unknown keys differently and the strict-key
// check has to hold for both.

// shiftTOML wraps a [team.role.shift] body in a minimal one-role TOML
// manifest. mode, when set, goes under runtime.
func shiftTOML(mode, shift string) string {
	m := ""
	if mode != "" {
		m = "    mode = \"" + mode + "\"\n"
	}
	return `
[workspace]
name = "test"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "claude"
` + m + `
    [team.role.shift]
` + shift
}

// shiftYAML wraps a shift mapping body (already indented under "shift:") in a
// minimal one-role YAML manifest. mode, when set, goes under runtime.
func shiftYAML(mode, shift string) string {
	m := ""
	if mode != "" {
		m = "          mode: " + mode + "\n"
	}
	return `
workspace:
  name: test

teams:
  - name: squad
    roles:
      - name: worker
        replicas: 1
        runtime:
          command: claude
` + m + `        shift:
` + shift
}

func applyShift(t *testing.T, src string) *ShiftPolicy {
	t.Helper()
	m, err := ParseManifestBytes([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	team, err := store.GetTeam("test/squad")
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	return team.Roles[0].Shift
}

// Item 1: today's single-trigger manifest still parses and normalizes to a
// list of one, keeping the single-form fields every existing reader uses.
func TestShiftSingleFormNormalizesToListOfOne(t *testing.T) {
	t.Parallel()
	for name, src := range map[string]string{
		"toml": shiftTOML("", "    on = \"context-pressure\"\n    headroom_tokens = 120000\n"),
		"yaml": shiftYAML("", "          on: context-pressure\n          headroom_tokens: 120000\n"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := applyShift(t, src)
			if p == nil {
				t.Fatal("expected a shift policy")
			}
			if p.On != ShiftTriggerContextPressure || p.HeadroomTokens != 120000 {
				t.Fatalf("single form = (%q, %d), want (context-pressure, 120000) kept", p.On, p.HeadroomTokens)
			}
			want := []ShiftCondition{{On: ShiftTriggerContextPressure, HeadroomTokens: 120000}}
			if got := p.Any; len(got) != 1 || got[0] != want[0] {
				t.Fatalf("Any = %+v, want %+v", got, want)
			}
		})
	}
}

// A policy persisted before the list existed has only On and HeadroomTokens;
// Conditions must still read it as a list of one.
func TestShiftConditionsReadsPersistedSingleForm(t *testing.T) {
	t.Parallel()
	p := &ShiftPolicy{On: ShiftTriggerContextPressure, HeadroomTokens: 5}
	got := p.Conditions()
	if len(got) != 1 || got[0].On != ShiftTriggerContextPressure || got[0].HeadroomTokens != 5 {
		t.Fatalf("Conditions() = %+v, want one context-pressure condition", got)
	}
	var nilPolicy *ShiftPolicy
	if nilPolicy.Conditions() != nil {
		t.Fatal("nil policy must have no conditions")
	}
}

func TestShiftListFormParsesWithDurationsAndHandoff(t *testing.T) {
	t.Parallel()
	for name, src := range map[string]string{
		"toml": shiftTOML("", `    handoff = "/var/handoffs/{session}.md"
    handoff_marker = "END HANDOFF"
    handoff_window = "10m"
    any = [
      { on = "context-pressure", headroom_tokens = 120000 },
      { on = "max-age", max_age = "8h", quiet_for = "3m", max_defer = "45m" },
    ]
`),
		"yaml": shiftYAML("", `          handoff: /var/handoffs/{session}.md
          handoff_marker: END HANDOFF
          handoff_window: 10m
          any:
            - on: context-pressure
              headroom_tokens: 120000
            - on: max-age
              max_age: 8h
              quiet_for: 3m
              max_defer: 45m
`),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := applyShift(t, src)
			want := []ShiftCondition{
				{On: ShiftTriggerContextPressure, HeadroomTokens: 120000},
				{On: ShiftTriggerMaxAge, MaxAge: 8 * time.Hour, QuietFor: 3 * time.Minute, MaxDefer: 45 * time.Minute},
			}
			if len(p.Any) != len(want) {
				t.Fatalf("Any = %+v, want %+v", p.Any, want)
			}
			for i := range want {
				if p.Any[i] != want[i] {
					t.Fatalf("Any[%d] = %+v, want %+v", i, p.Any[i], want[i])
				}
			}
			if p.Handoff != "/var/handoffs/{session}.md" || p.HandoffMarker != "END HANDOFF" || p.HandoffWindow != 10*time.Minute {
				t.Fatalf("handoff = (%q, %q, %v)", p.Handoff, p.HandoffMarker, p.HandoffWindow)
			}
		})
	}
}

// The single form takes max-age too: a list of one.
func TestShiftSingleFormMaxAge(t *testing.T) {
	t.Parallel()
	p := applyShift(t, shiftTOML("", "    on = \"max-age\"\n    max_age = \"8h\"\n"))
	if len(p.Any) != 1 || p.Any[0].On != ShiftTriggerMaxAge || p.Any[0].MaxAge != 8*time.Hour {
		t.Fatalf("Any = %+v, want one max-age 8h", p.Any)
	}
}

// Items 2 and 3: every malformed shape is an error at parse that names what
// is wrong. Never a silent no-op.
func TestShiftListRejections(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		toml string
		yaml string
		mode string // runtime mode, for the headless case
		want string // substring the error must contain
	}{
		{
			name: "table-level action",
			toml: "    on = \"max-age\"\n    max_age = \"8h\"\n    action = \"kill\"\n",
			yaml: "          on: max-age\n          max_age: 8h\n          action: kill\n",
			want: `"action" is not supported by this marvel`,
		},
		{
			name: "table-level all",
			toml: "    all = [ { on = \"max-age\", max_age = \"8h\" } ]\n",
			yaml: "          all:\n            - on: max-age\n              max_age: 8h\n",
			want: `"all" is not supported by this marvel`,
		},
		{
			name: "entry-level unknown key",
			toml: "    any = [ { on = \"max-age\", max_age = \"8h\", bogus = 1 } ]\n",
			yaml: "          any:\n            - on: max-age\n              max_age: 8h\n              bogus: 1\n",
			want: `"bogus" is not supported by this marvel`,
		},
		{
			name: "nested any",
			toml: "    any = [ { any = [ { on = \"max-age\", max_age = \"8h\" } ] } ]\n",
			yaml: "          any:\n            - any:\n                - on: max-age\n                  max_age: 8h\n",
			want: `"any" is not supported by this marvel`,
		},
		{
			name: "neither on nor any",
			toml: "    handoff_window = \"5m\"\n",
			yaml: "          handoff_window: 5m\n",
			want: "needs on or any",
		},
		{
			name: "on with any",
			toml: "    on = \"max-age\"\n    max_age = \"8h\"\n    any = [ { on = \"context-pressure\", headroom_tokens = 1 } ]\n",
			yaml: "          on: max-age\n          max_age: 8h\n          any:\n            - on: context-pressure\n              headroom_tokens: 1\n",
			want: "on and any together",
		},
		{
			name: "empty any",
			toml: "    any = []\n",
			yaml: "          any: []\n",
			want: "any is empty",
		},
		{
			name: "duplicate on",
			toml: "    any = [ { on = \"max-age\", max_age = \"8h\" }, { on = \"max-age\", max_age = \"9h\" } ]\n",
			yaml: "          any:\n            - on: max-age\n              max_age: 8h\n            - on: max-age\n              max_age: 9h\n",
			want: `"max-age" appears twice`,
		},
		{
			name: "unknown on",
			toml: "    on = \"idle\"\n",
			yaml: "          on: idle\n",
			want: `"idle" is not valid`,
		},
		{
			name: "max_age below floor",
			toml: "    on = \"max-age\"\n    max_age = \"8m\"\n",
			yaml: "          on: max-age\n          max_age: 8m\n",
			want: "at least 15m",
		},
		{
			name: "max_age missing",
			toml: "    on = \"max-age\"\n",
			yaml: "          on: max-age\n",
			want: "max_age is required",
		},
		{
			name: "max_age unparseable",
			toml: "    on = \"max-age\"\n    max_age = \"8 hours\"\n",
			yaml: "          on: max-age\n          max_age: 8 hours\n",
			want: "max_age",
		},
		{
			name: "headroom on a max-age entry",
			toml: "    on = \"max-age\"\n    max_age = \"8h\"\n    headroom_tokens = 5\n",
			yaml: "          on: max-age\n          max_age: 8h\n          headroom_tokens: 5\n",
			want: "headroom_tokens does not apply",
		},
		{
			name: "max_age on a context-pressure entry",
			toml: "    on = \"context-pressure\"\n    headroom_tokens = 5\n    max_age = \"8h\"\n",
			yaml: "          on: context-pressure\n          headroom_tokens: 5\n          max_age: 8h\n",
			want: "max_age does not apply",
		},
		{
			name: "handoff without marker",
			toml: "    on = \"max-age\"\n    max_age = \"8h\"\n    handoff = \"/tmp/{session}.md\"\n",
			yaml: "          on: max-age\n          max_age: 8h\n          handoff: /tmp/{session}.md\n",
			want: "handoff and handoff_marker",
		},
		{
			name: "relative handoff path",
			toml: "    on = \"max-age\"\n    max_age = \"8h\"\n    handoff = \"handoffs/{session}.md\"\n    handoff_marker = \"END\"\n",
			yaml: "          on: max-age\n          max_age: 8h\n          handoff: handoffs/{session}.md\n          handoff_marker: END\n",
			want: "must be absolute",
		},
		{
			name: "dot-dot in handoff path",
			toml: "    on = \"max-age\"\n    max_age = \"8h\"\n    handoff = \"~/../../etc/{session}.md\"\n    handoff_marker = \"END\"\n",
			yaml: "          on: max-age\n          max_age: 8h\n          handoff: ~/../../etc/{session}.md\n          handoff_marker: END\n",
			want: "must not contain a .. element",
		},
		{
			name: "max-age on a headless role",
			toml: "    on = \"max-age\"\n    max_age = \"8h\"\n",
			yaml: "          on: max-age\n          max_age: 8h\n",
			mode: "headless",
			want: "headless",
		},
	}
	for _, tc := range cases {
		tc := tc
		srcs := map[string]string{
			"toml": shiftTOML(tc.mode, tc.toml),
			"yaml": shiftYAML(tc.mode, tc.yaml),
		}
		for format, src := range srcs {
			format, src := format, src
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				t.Parallel()
				_, err := ParseManifestBytes([]byte(src))
				if err == nil {
					t.Fatalf("parse succeeded; want an error containing %q", tc.want)
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("error = %q, want it to contain %q", err, tc.want)
				}
				if !strings.Contains(err.Error(), "shift") {
					t.Fatalf("error = %q does not say it is about the shift table", err)
				}
			})
		}
	}
}

// A pending handoff request, the list policy and a successor's lineage live
// in the durable record, so a daemon restart inside the window resumes the
// request (design item 9) and an adopted successor keeps its lineage.
func TestShiftRequestAndLineageSurviveBolt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	asked := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)

	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #1: %v", err)
	}
	if err := s1.CreateWorkspace(&Workspace{Name: "ws", CreatedAt: asked}); err != nil {
		t.Fatal(err)
	}
	policy := &ShiftPolicy{
		Any:           []ShiftCondition{{On: ShiftTriggerMaxAge, MaxAge: 8 * time.Hour}},
		Handoff:       "/var/h/{session}.md",
		HandoffMarker: "END",
	}
	if err := s1.CreateTeam(&Team{
		Name: "squad", Workspace: "ws", CreatedAt: asked,
		Roles:         []Role{{Name: "worker", Replicas: 1, Runtime: Runtime{Command: "sleep"}, Shift: policy}},
		ShiftRequests: map[string]ShiftRequest{"worker": {Session: "ws/squad-worker-g1-0", Cause: ShiftTriggerMaxAge, RequestedAt: asked, NoticeUndelivered: "refused: update menu"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s1.CreateSession(&Session{
		Name: "squad-worker-g2-0", Workspace: "ws", Team: "squad", Role: "worker", Generation: 2,
		Runtime: Runtime{Command: "sleep"}, Predecessor: "ws/squad-worker-g1-0", HandoffRequestedAt: asked,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s1.CloseBolt(); err != nil {
		t.Fatal(err)
	}

	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #2: %v", err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	team, err := s2.GetTeam("ws/squad")
	if err != nil {
		t.Fatal(err)
	}
	if req := team.ShiftRequests["worker"]; req.Session != "ws/squad-worker-g1-0" || !req.RequestedAt.Equal(asked) || req.NoticeUndelivered != "refused: update menu" {
		t.Fatalf("ShiftRequests after rehydrate = %+v", team.ShiftRequests)
	}
	if got := team.Roles[0].Shift; got == nil || len(got.Any) != 1 || got.Any[0].MaxAge != 8*time.Hour || got.Handoff != policy.Handoff {
		t.Fatalf("Shift policy after rehydrate = %+v", got)
	}
	sess, err := s2.GetSession("ws/squad-worker-g2-0")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Predecessor != "ws/squad-worker-g1-0" || !sess.HandoffRequestedAt.Equal(asked) {
		t.Fatalf("lineage after rehydrate = (%q, %v)", sess.Predecessor, sess.HandoffRequestedAt)
	}
}

// Snapshots of a team do not share the new maps or the policy's list with the
// store (go.md rule 12).
func TestCloneTeamCopiesShiftRequestsAndPolicy(t *testing.T) {
	t.Parallel()
	s := NewStore()
	if err := s.CreateWorkspace(&Workspace{Name: "ws", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateTeam(&Team{
		Name: "squad", Workspace: "ws", CreatedAt: time.Now().UTC(),
		Roles: []Role{{
			Name: "worker", Replicas: 1, Runtime: Runtime{Command: "sleep"},
			Shift: &ShiftPolicy{Any: []ShiftCondition{{On: ShiftTriggerMaxAge, MaxAge: time.Hour}}},
		}},
		ShiftRequests: map[string]ShiftRequest{"worker": {Session: "a"}},
		Shift:         ShiftState{HandoffRequests: map[string]ShiftRequest{"worker": {Session: "a"}}},
	}); err != nil {
		t.Fatal(err)
	}
	snap, err := s.GetTeam("ws/squad")
	if err != nil {
		t.Fatal(err)
	}
	snap.ShiftRequests["worker"] = ShiftRequest{Session: "mutated"}
	snap.Shift.HandoffRequests["worker"] = ShiftRequest{Session: "mutated"}
	snap.Roles[0].Shift.Any[0].MaxAge = 0
	snap.Roles[0].Shift.Handoff = "mutated"

	live, err := s.GetTeam("ws/squad")
	if err != nil {
		t.Fatal(err)
	}
	if live.ShiftRequests["worker"].Session != "a" || live.Shift.HandoffRequests["worker"].Session != "a" {
		t.Fatal("a snapshot's request maps alias the store's")
	}
	if live.Roles[0].Shift.Any[0].MaxAge != time.Hour || live.Roles[0].Shift.Handoff != "" {
		t.Fatal("a snapshot's shift policy aliases the store's")
	}
}

// A max-age request is keyed by role and asks one seat, but the shift it
// starts drains every seat of the role, so on a role with several replicas it
// would retire seats that were never asked for a handoff (marvel#452). Until
// per-seat requests are designed, apply refuses it, as it does for headless.
func TestMaxAgeRefusedOnAMultiReplicaRole(t *testing.T) {
	maxAgeTOML := "    on = \"max-age\"\n    max_age = \"8h\"\n"
	maxAgeYAML := "          on: max-age\n          max_age: 8h\n"
	pressureTOML := "    on = \"context-pressure\"\n    headroom_tokens = 5\n"
	pressureYAML := "          on: context-pressure\n          headroom_tokens: 5\n"
	withReplicas := func(src string, n int, format string) string {
		if format == "toml" {
			return strings.Replace(src, "replicas = 1", fmt.Sprintf("replicas = %d", n), 1)
		}
		return strings.Replace(src, "replicas: 1", fmt.Sprintf("replicas: %d", n), 1)
	}
	cases := []struct {
		name     string
		replicas int
		toml     string
		yaml     string
		want     string // empty means the manifest must be accepted
	}{
		{"max-age on three replicas is refused", 3, maxAgeTOML, maxAgeYAML, "replicas"},
		{"max-age on one replica is accepted", 1, maxAgeTOML, maxAgeYAML, ""},
		{"max-age on a parked role is accepted", 0, maxAgeTOML, maxAgeYAML, ""},
		{"context-pressure on three replicas is accepted", 3, pressureTOML, pressureYAML, ""},
	}
	for _, tc := range cases {
		for _, format := range []string{"toml", "yaml"} {
			tc, format := tc, format
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				t.Parallel()
				src := shiftTOML("", tc.toml)
				if format == "yaml" {
					src = shiftYAML("", tc.yaml)
				}
				_, err := ParseManifestBytes([]byte(withReplicas(src, tc.replicas, format)))
				if tc.want == "" {
					if err != nil {
						t.Fatalf("parse: %v; want it accepted", err)
					}
					return
				}
				if err == nil {
					t.Fatalf("parse succeeded; want an error containing %q", tc.want)
				}
				if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "shift") {
					t.Fatalf("error = %q, want it about the shift table and %q", err, tc.want)
				}
			})
		}
	}
}

// A view notice persists with the team, so a restart neither forgets an
// undelivered notice nor starts a grace early, and a snapshot does not alias the
// store's map.
func TestViewNoticesPersistAndAreCloned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.db")
	at := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	notice := ViewNotice{
		Commit: "c2", Path: "/v/ws/s/repo/cur", PendingSince: at, Undelivered: "refused: update menu",
		DeliveredCommit: "c1", DeliveredAt: at.Add(-time.Hour), GraceStart: at.Add(-time.Hour + time.Minute),
		Superseded: []string{"c0", "c1"}, Sealed: []string{"c-1"},
	}

	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	if err := s1.CreateWorkspace(&Workspace{Name: "ws", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := s1.CreateTeam(&Team{
		Name: "squad", Workspace: "ws", CreatedAt: at,
		Roles:       []Role{{Name: "worker", Replicas: 1, Runtime: Runtime{Command: "sleep"}}},
		ViewNotices: map[string]ViewNotice{"ws/squad-worker-g1-0/repo": notice},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s1.CloseBolt(); err != nil {
		t.Fatal(err)
	}

	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	team, err := s2.GetTeam("ws/squad")
	if err != nil {
		t.Fatal(err)
	}
	if got := team.ViewNotices["ws/squad-worker-g1-0/repo"]; got.Commit != notice.Commit || !reflect.DeepEqual(got, notice) {
		t.Fatalf("notice after rehydrate = %+v, want %+v", got, notice)
	}
	team.ViewNotices["ws/squad-worker-g1-0/repo"].Superseded[0] = "mutated"
	team.ViewNotices["ws/squad-worker-g1-0/repo"].Sealed[0] = "mutated"
	team.ViewNotices["ws/squad-worker-g1-0/repo"] = ViewNotice{Commit: "mutated"}
	live, _ := s2.GetTeam("ws/squad")
	if live.ViewNotices["ws/squad-worker-g1-0/repo"].Commit != "c2" {
		t.Fatal("a snapshot's notice map aliases the store's")
	}
	if got := live.ViewNotices["ws/squad-worker-g1-0/repo"]; got.Superseded[0] != "c0" || got.Sealed[0] != "c-1" {
		t.Fatalf("a snapshot's tree lists alias the store's: %+v", got)
	}
}
