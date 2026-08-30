package api

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const validManifest = `
[workspace]
name = "test-project"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 3

    [team.role.runtime]
    command = "bash"
    args = ["-c", "while true; do sleep 1; done"]

  [[team.role]]
  name = "monitor"
  replicas = 1

    [team.role.runtime]
    image = "top"
    command = "top"

[[endpoint]]
name = "agent-svc"
team = "squad"
`

func TestParseManifest(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(validManifest))
	if err != nil {
		t.Fatalf("parse valid manifest: %v", err)
	}
	if m.Workspace.Name != "test-project" {
		t.Fatalf("expected workspace test-project, got %s", m.Workspace.Name)
	}
	if len(m.Teams) != 1 {
		t.Fatalf("expected 1 team, got %d", len(m.Teams))
	}
	if m.Teams[0].Name != "squad" {
		t.Fatalf("expected team squad, got %s", m.Teams[0].Name)
	}
	if len(m.Teams[0].Roles) != 2 {
		t.Fatalf("expected 2 roles, got %d", len(m.Teams[0].Roles))
	}
	if m.Teams[0].Roles[0].Name != "worker" {
		t.Fatalf("expected role worker, got %s", m.Teams[0].Roles[0].Name)
	}
	if m.Teams[0].Roles[0].Replicas != 3 {
		t.Fatalf("expected 3 replicas, got %d", m.Teams[0].Roles[0].Replicas)
	}
	if len(m.Endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(m.Endpoints))
	}
}

func TestParseManifestMissingWorkspace(t *testing.T) {
	t.Parallel()
	_, err := ParseManifestBytes([]byte(`
[[team]]
name = "agents"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "bash"
`))
	if err == nil {
		t.Fatal("expected error for missing workspace name")
	}
}

func TestParseManifestNoRoles(t *testing.T) {
	t.Parallel()
	_, err := ParseManifestBytes([]byte(`
[workspace]
name = "test"

[[team]]
name = "agents"
`))
	if err == nil {
		t.Fatal("expected error for team with no roles")
	}
}

func TestParseManifestBadReplicas(t *testing.T) {
	t.Parallel()
	_, err := ParseManifestBytes([]byte(`
[workspace]
name = "test"

[[team]]
name = "agents"

  [[team.role]]
  name = "worker"
  replicas = 0

    [team.role.runtime]
    command = "bash"
`))
	if err == nil {
		t.Fatal("expected error for zero replicas")
	}
}

func TestParseManifestMultipleRoles(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(`
[workspace]
name = "test"

[[team]]
name = "squad"

  [[team.role]]
  name = "supervisor"
  replicas = 1

    [team.role.runtime]
    image = "simulator"
    command = "simulator"
    script = "scripts/chaos.lua"

  [[team.role]]
  name = "worker"
  replicas = 5

    [team.role.runtime]
    image = "simulator"
    command = "simulator"
`))
	if err != nil {
		t.Fatalf("parse manifest with multiple roles: %v", err)
	}
	if len(m.Teams[0].Roles) != 2 {
		t.Fatalf("expected 2 roles, got %d", len(m.Teams[0].Roles))
	}
	if m.Teams[0].Roles[0].Name != "supervisor" {
		t.Fatalf("expected supervisor, got %s", m.Teams[0].Roles[0].Name)
	}
	if m.Teams[0].Roles[0].Runtime.Script != "scripts/chaos.lua" {
		t.Fatalf("expected script path, got %s", m.Teams[0].Roles[0].Runtime.Script)
	}
	if m.Teams[0].Roles[1].Replicas != 5 {
		t.Fatalf("expected 5 replicas, got %d", m.Teams[0].Roles[1].Replicas)
	}
}

func TestParseManifestWithHealthcheck(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(`
[workspace]
name = "test"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 2
  restart_policy = "on-failure"

    [team.role.runtime]
    command = "bash"

    [team.role.healthcheck]
    type = "heartbeat"
    timeout = "15s"
    failure_threshold = 5
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	team, _ := store.GetTeam("test/squad")
	role := team.Roles[0]
	if role.RestartPolicy != RestartOnFailure {
		t.Fatalf("expected on-failure, got %s", role.RestartPolicy)
	}
	if role.HealthCheck == nil {
		t.Fatal("expected healthcheck")
	}
	if role.HealthCheck.Type != HealthCheckHeartbeat {
		t.Fatalf("expected heartbeat, got %s", role.HealthCheck.Type)
	}
	if role.HealthCheck.Timeout != 15*time.Second {
		t.Fatalf("expected 15s timeout, got %v", role.HealthCheck.Timeout)
	}
	if role.HealthCheck.FailureThreshold != 5 {
		t.Fatalf("expected threshold 5, got %d", role.HealthCheck.FailureThreshold)
	}
}

func TestParseManifestWithActivityTimeout(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(`
[workspace]
name = "test"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1
  activity_timeout = "10m"

    [team.role.runtime]
    command = "bash"
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	team, _ := store.GetTeam("test/squad")
	if got := team.Roles[0].ActivityTimeout; got != 10*time.Minute {
		t.Fatalf("expected 10m activity_timeout, got %v", got)
	}
}

func TestParseManifestActivityTimeoutDefaultsDisabled(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(`
[workspace]
name = "test"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "bash"
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	team, _ := store.GetTeam("test/squad")
	if got := team.Roles[0].ActivityTimeout; got != 0 {
		t.Fatalf("unset activity_timeout must default to 0 (disabled), got %v", got)
	}
}

func TestParseManifestBadActivityTimeout(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(`
[workspace]
name = "test"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1
  activity_timeout = "soon"

    [team.role.runtime]
    command = "bash"
`))
	if err != nil {
		t.Fatalf("parse should succeed (the duration is validated at apply): %v", err)
	}
	err = m.Apply(NewStore())
	if err == nil {
		t.Fatal("expected an error for an unparseable activity_timeout")
	}
	if !strings.Contains(err.Error(), "activity_timeout") {
		t.Fatalf("error should name activity_timeout, got %v", err)
	}
}

// --- YAML format tests ---

const validYAMLManifest = `
workspace:
  name: test-project

teams:
  - name: squad
    roles:
      - name: worker
        replicas: 3
        runtime:
          command: bash
          args: ["-c", "while true; do sleep 1; done"]
      - name: monitor
        replicas: 1
        runtime:
          image: top
          command: top

endpoints:
  - name: agent-svc
    team: squad
`

func TestParseYAMLManifest(t *testing.T) {
	t.Parallel()
	m, err := parseManifestYAML([]byte(validYAMLManifest))
	if err != nil {
		t.Fatalf("parse valid yaml manifest: %v", err)
	}
	if m.Workspace.Name != "test-project" {
		t.Fatalf("expected workspace test-project, got %s", m.Workspace.Name)
	}
	if len(m.Teams) != 1 {
		t.Fatalf("expected 1 team, got %d", len(m.Teams))
	}
	if m.Teams[0].Name != "squad" {
		t.Fatalf("expected team squad, got %s", m.Teams[0].Name)
	}
	if len(m.Teams[0].Roles) != 2 {
		t.Fatalf("expected 2 roles, got %d", len(m.Teams[0].Roles))
	}
	if m.Teams[0].Roles[0].Name != "worker" {
		t.Fatalf("expected role worker, got %s", m.Teams[0].Roles[0].Name)
	}
	if m.Teams[0].Roles[0].Replicas != 3 {
		t.Fatalf("expected 3 replicas, got %d", m.Teams[0].Roles[0].Replicas)
	}
	if len(m.Endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(m.Endpoints))
	}
}

// TestParseManifestBytesReportsYAMLValidationErrors covers the only apply
// path there is: `marvel work` sends bytes, so every manifest the daemon sees
// arrives through ParseManifestBytes.
//
// Validating inside each format attempt made ANY validation failure on a YAML
// manifest fall through to the TOML parser, so the operator was told
// "toml: line N: expected '.' or '=', but got ':' instead" instead of which
// rule they broke. It masked every rule equally, which is why the rows below
// include one that predates budgets.
func TestParseManifestBytesReportsYAMLValidationErrors(t *testing.T) {
	t.Parallel()
	yamlWith := func(budget, replicas string) string {
		return `
workspace:
  name: fanout

teams:
  - name: crew
    budget:
` + budget + `
    roles:
      - name: crew
        replicas: ` + replicas + `
        runtime:
          image: generic
          command: sleep
`
	}
	tests := []struct {
		name     string
		manifest string
		wantErr  string
	}{
		{
			name:     "the declaration clause",
			manifest: yamlWith("      max_sessions: 6", "40"),
			wantErr:  "declares 40 replicas across 1 role(s) but budget.max_sessions is 6",
		},
		{
			name:     "a registered but unenforced dimension",
			manifest: yamlWith("      max_cost_usd: 5.0", "1"),
			wantErr:  "is a known dimension (matrix row",
		},
		{
			name:     "an on_unmeasured typo",
			manifest: yamlWith("      max_sessions: 6\n      on_unmeasured: deny", "1"),
			wantErr:  `on_unmeasured "deny" is not valid`,
		},
		{
			name:     "a rule that predates budgets",
			manifest: yamlWith("      max_sessions: 6", "0"),
			wantErr:  "replicas must be >= 1",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseManifestBytes([]byte(tt.manifest))
			if err == nil {
				t.Fatalf("expected an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), "toml") {
				t.Errorf("error = %q, want no TOML complaint about a YAML manifest", err)
			}
		})
	}
}

// TestParseManifestBytesStillAcceptsTOML is the other half of the format
// decision above: settling the format on the required field rather than on
// unmarshal success is what keeps TOML working, since yaml.Unmarshal tolerates
// some TOML input and yields a manifest with nothing in it.
func TestParseManifestBytesStillAcceptsTOML(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(validManifest))
	if err != nil {
		t.Fatalf("parse TOML through the bytes path: %v", err)
	}
	if m.Workspace.Name != "test-project" || len(m.Teams) != 1 {
		t.Fatalf("manifest = %+v, want the TOML fixture's workspace and team", m)
	}
	// A TOML validation failure keeps naming the rule, not the format.
	_, err = ParseManifestBytes([]byte(`
[workspace]
name = "fanout"

[[team]]
name = "crew"

  [team.budget]
  max_sessions = 6

  [[team.role]]
  name = "crew"
  replicas = 40

    [team.role.runtime]
    command = "sleep"
`))
	if err == nil {
		t.Fatal("expected the declaration clause to refuse a TOML manifest too")
	}
	if !strings.Contains(err.Error(), "budget.max_sessions is 6") {
		t.Errorf("error = %q, want the declaration clause", err)
	}
}

func TestParseYAMLManifestWithHealthcheck(t *testing.T) {
	t.Parallel()
	m, err := parseManifestYAML([]byte(`
workspace:
  name: test

teams:
  - name: squad
    roles:
      - name: worker
        replicas: 2
        restart_policy: on-failure
        permissions: plan
        runtime:
          image: claude
          command: claude
        healthcheck:
          type: heartbeat
          timeout: "15s"
          failure_threshold: 5
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	team, _ := store.GetTeam("test/squad")
	role := team.Roles[0]
	if role.RestartPolicy != RestartOnFailure {
		t.Fatalf("expected on-failure, got %s", role.RestartPolicy)
	}
	if role.Permissions != "plan" {
		t.Fatalf("expected plan permissions, got %s", role.Permissions)
	}
	if role.HealthCheck == nil {
		t.Fatal("expected healthcheck")
	}
	if role.HealthCheck.Timeout != 15*time.Second {
		t.Fatalf("expected 15s timeout, got %v", role.HealthCheck.Timeout)
	}
}

func TestParseYAMLManifestMultipleRoles(t *testing.T) {
	t.Parallel()
	m, err := parseManifestYAML([]byte(`
workspace:
  name: test

teams:
  - name: squad
    roles:
      - name: supervisor
        replicas: 1
        permissions: auto
        runtime:
          image: forestage
          command: forestage
          args: ["--persona", "dune/supervisor"]

      - name: worker
        replicas: 3
        permissions: plan
        runtime:
          image: claude
          command: claude
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(m.Teams[0].Roles) != 2 {
		t.Fatalf("expected 2 roles, got %d", len(m.Teams[0].Roles))
	}
	if m.Teams[0].Roles[0].Permissions != "auto" {
		t.Fatalf("expected auto permissions, got %s", m.Teams[0].Roles[0].Permissions)
	}
	if m.Teams[0].Roles[0].Runtime.Args[0] != "--persona" {
		t.Fatalf("expected --persona arg, got %s", m.Teams[0].Roles[0].Runtime.Args[0])
	}
}

func TestParseManifestBytesAutoDetect(t *testing.T) {
	t.Parallel()

	// YAML input should parse successfully
	yamlM, err := ParseManifestBytes([]byte(validYAMLManifest))
	if err != nil {
		t.Fatalf("ParseManifestBytes with YAML: %v", err)
	}
	if yamlM.Workspace.Name != "test-project" {
		t.Fatalf("YAML: expected test-project, got %s", yamlM.Workspace.Name)
	}

	// TOML input should also parse successfully (fallback)
	tomlM, err := ParseManifestBytes([]byte(validManifest))
	if err != nil {
		t.Fatalf("ParseManifestBytes with TOML: %v", err)
	}
	if tomlM.Workspace.Name != "test-project" {
		t.Fatalf("TOML: expected test-project, got %s", tomlM.Workspace.Name)
	}
}

func TestManifestApply(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(validManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Workspace created
	if _, err := store.GetWorkspace("test-project"); err != nil {
		t.Fatalf("workspace not created: %v", err)
	}

	// Teams created
	teams := store.ListTeams()
	if len(teams) != 1 {
		t.Fatalf("expected 1 team, got %d", len(teams))
	}

	squad, err := store.GetTeam("test-project/squad")
	if err != nil {
		t.Fatalf("get squad team: %v", err)
	}
	if len(squad.Roles) != 2 {
		t.Fatalf("expected 2 roles, got %d", len(squad.Roles))
	}
	if squad.Roles[0].Replicas != 3 {
		t.Fatalf("expected 3 replicas for worker, got %d", squad.Roles[0].Replicas)
	}
	if squad.Generation != 1 {
		t.Fatalf("expected generation 1 for new team, got %d", squad.Generation)
	}

	// Endpoint created
	eps := store.ListEndpoints()
	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(eps))
	}

	// Idempotent re-apply
	if err := m.Apply(store); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
}

// TestParseYAMLManifestDroppedFields is the regression test for
// ArcavenAE/marvel#28 (max_restarts) and #43 (dangerous_permissions).
// Both fields existed on api.Role and were honored by the team
// controller and forestage adapter respectively — but ManifestRole
// didn't declare them, so yaml.v3 silently dropped them during parse,
// and Apply() never copied them onto Role. Effect: the cap was
// permanently disabled and --dangerously-skip-permissions never made
// it to the adapter.
func TestParseYAMLManifestDroppedFields(t *testing.T) {
	t.Parallel()
	m, err := parseManifestYAML([]byte(`
workspace:
  name: test

teams:
  - name: squad
    roles:
      - name: worker
        replicas: 1
        max_restarts: 3
        dangerous_permissions: true
        runtime:
          image: forestage
          command: forestage
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	role := m.Teams[0].Roles[0]
	if role.MaxRestarts != 3 {
		t.Errorf("MaxRestarts on ManifestRole: got %d, want 3", role.MaxRestarts)
	}
	if !role.DangerousPermissions {
		t.Errorf("DangerousPermissions on ManifestRole: got false, want true")
	}

	// Full round-trip to api.Role via Apply must carry both fields.
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	team, err := store.GetTeam("test/squad")
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	if team.Roles[0].MaxRestarts != 3 {
		t.Errorf("MaxRestarts on api.Role after Apply: got %d, want 3", team.Roles[0].MaxRestarts)
	}
	if !team.Roles[0].DangerousPermissions {
		t.Errorf("DangerousPermissions on api.Role after Apply: got false, want true")
	}
}

// TestParseTOMLManifestDroppedFields is the TOML-side twin of
// TestParseYAMLManifestDroppedFields. TOML was already honoring the
// toml struct tags on api.Role directly (Role is used in some code
// paths without going through ManifestRole), but the manifest parse
// path is the same — ManifestRole was missing the fields, so TOML
// manifests silently dropped them too.
func TestParseTOMLManifestDroppedFields(t *testing.T) {
	t.Parallel()
	m, err := parseManifestTOML([]byte(`
[workspace]
name = "test"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1
  max_restarts = 3
  dangerous_permissions = true

    [team.role.runtime]
    image = "forestage"
    command = "forestage"
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	role := m.Teams[0].Roles[0]
	if role.MaxRestarts != 3 {
		t.Errorf("MaxRestarts on ManifestRole: got %d, want 3", role.MaxRestarts)
	}
	if !role.DangerousPermissions {
		t.Errorf("DangerousPermissions on ManifestRole: got false, want true")
	}

	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	team, err := store.GetTeam("test/squad")
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	if team.Roles[0].MaxRestarts != 3 {
		t.Errorf("MaxRestarts on api.Role after Apply: got %d, want 3", team.Roles[0].MaxRestarts)
	}
	if !team.Roles[0].DangerousPermissions {
		t.Errorf("DangerousPermissions on api.Role after Apply: got false, want true")
	}
}

// TestParseManifestDroppedFieldsDefaults verifies that omitting both
// fields produces zero values (MaxRestarts=0 meaning unlimited,
// DangerousPermissions=false meaning the adapter does not append the
// flag). Guards against accidental non-zero defaults that would break
// the documented contract.
func TestParseManifestDroppedFieldsDefaults(t *testing.T) {
	t.Parallel()
	m, err := parseManifestYAML([]byte(`
workspace:
  name: test

teams:
  - name: squad
    roles:
      - name: worker
        replicas: 1
        runtime:
          command: sleep
          args: ["300"]
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	team, _ := store.GetTeam("test/squad")
	if team.Roles[0].MaxRestarts != 0 {
		t.Errorf("MaxRestarts default: got %d, want 0", team.Roles[0].MaxRestarts)
	}
	if team.Roles[0].DangerousPermissions {
		t.Errorf("DangerousPermissions default: got true, want false")
	}
}

// TestParseYAMLManifestBudget extends the DroppedFields trio to the budget
// block: yaml.v3 drops fields the struct does not declare, so a budget that
// parses into nothing would be an accepted no-op gate.
func TestParseYAMLManifestBudget(t *testing.T) {
	t.Parallel()
	m, err := parseManifestYAML([]byte(`
workspace:
  name: fanout

teams:
  - name: crew
    budget:
      max_sessions: 6
      max_tokens: 2000000
      on_unmeasured: refuse
    roles:
      - name: crew
        replicas: 3
        runtime:
          command: sleep
          args: ["300"]
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	mb := m.Teams[0].Budget
	if mb == nil {
		t.Fatal("budget block on ManifestTeam: got nil, want the declared block")
	}
	if mb.MaxSessions != 6 {
		t.Errorf("MaxSessions on ManifestBudget: got %d, want 6", mb.MaxSessions)
	}
	if mb.MaxTokens != 2000000 {
		t.Errorf("MaxTokens on ManifestBudget: got %d, want 2000000", mb.MaxTokens)
	}
	if mb.OnUnmeasured != UnmeasuredRefuse {
		t.Errorf("OnUnmeasured on ManifestBudget: got %q, want %q", mb.OnUnmeasured, UnmeasuredRefuse)
	}

	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	team, err := store.GetTeam("fanout/crew")
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	if team.Budget.MaxSessions != 6 || team.Budget.MaxTokens != 2000000 {
		t.Errorf("Budget on api.Team after Apply: got %+v", team.Budget)
	}
	if !team.Budget.Declared() {
		t.Error("a declared budget did not survive Apply")
	}
}

// TestParseTOMLManifestBudget is the TOML twin. Every example under
// examples/ ships as a pair, so both formats have to carry the block.
func TestParseTOMLManifestBudget(t *testing.T) {
	t.Parallel()
	m, err := parseManifestTOML([]byte(`
[workspace]
name = "fanout"

[[team]]
name = "crew"

  [team.budget]
  max_sessions = 6
  max_tokens = 2000000

  [[team.role]]
  name = "crew"
  replicas = 3

    [team.role.runtime]
    command = "sleep"
    args = ["300"]
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Teams[0].Budget == nil {
		t.Fatal("budget block on ManifestTeam: got nil, want the declared block")
	}
	if m.Teams[0].Budget.MaxSessions != 6 {
		t.Errorf("MaxSessions on ManifestBudget: got %d, want 6", m.Teams[0].Budget.MaxSessions)
	}

	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	team, _ := store.GetTeam("fanout/crew")
	if team.Budget.MaxTokens != 2000000 {
		t.Errorf("MaxTokens on api.Team after Apply: got %d, want 2000000", team.Budget.MaxTokens)
	}
}

// TestParseManifestBudgetDefaults verifies that omitting the block leaves a
// zero Budget, which declares no gate. This is the default-open guarantee
// for every manifest written before the feature existed.
func TestParseManifestBudgetDefaults(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(validManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Teams[0].Budget != nil {
		t.Errorf("absent budget block parsed as %+v, want nil", m.Teams[0].Budget)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	team, _ := store.GetTeam("test-project/squad")
	if team.Budget.Declared() {
		t.Errorf("a manifest with no budget block produced a gate: %+v", team.Budget)
	}
}

// TestReapplyCarriesAnEditedBudget is the fourth member of the quartet and
// the one that catches a real trap. Apply's existing-team branch copies only
// the fields it names: without live.Budget in that closure, an edited budget
// applies on create and is silently ignored on every re-apply, while an
// edited role list takes effect.
func TestReapplyCarriesAnEditedBudget(t *testing.T) {
	t.Parallel()
	manifest := func(maxSessions, replicas int) []byte {
		return []byte(fmt.Sprintf(`
workspace:
  name: fanout

teams:
  - name: crew
    budget:
      max_sessions: %d
    roles:
      - name: crew
        replicas: %d
        runtime:
          command: sleep
          args: ["300"]
`, maxSessions, replicas))
	}

	store := NewStore()
	first, err := parseManifestYAML(manifest(6, 3))
	if err != nil {
		t.Fatalf("parse #1: %v", err)
	}
	if err := first.Apply(store); err != nil {
		t.Fatalf("apply #1: %v", err)
	}

	second, err := parseManifestYAML(manifest(40, 8))
	if err != nil {
		t.Fatalf("parse #2: %v", err)
	}
	if err := second.Apply(store); err != nil {
		t.Fatalf("apply #2: %v", err)
	}

	team, err := store.GetTeam("fanout/crew")
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	if team.Roles[0].Replicas != 8 {
		t.Fatalf("re-apply dropped the edited role list: replicas = %d, want 8", team.Roles[0].Replicas)
	}
	if team.Budget.MaxSessions != 40 {
		t.Errorf("re-apply dropped the edited budget: max_sessions = %d, want 40", team.Budget.MaxSessions)
	}
}

// TestValidateBudgets covers the host-side pre-flight: a token ceiling on a
// team where no role can ever report a token is a mute gate, and a mute gate
// is an apply-time error rather than a silent no-op. Same class of fix as
// ArcavenAE/marvel#9.
//
// The predicate is injected because the answer lives in the adapter registry
// (internal/runtime imports this package). Mode alone is not the question: a
// role whose harness has no stream path satisfies every mode check and can
// still never report a token, which is the exact mute gate this rejects.
func TestValidateBudgets(t *testing.T) {
	t.Parallel()
	role := func(name string, mode RuntimeMode) ManifestRole {
		return ManifestRole{Name: name, Replicas: 1, Runtime: ManifestRuntime{Image: "claude", Command: "claude", Mode: mode}}
	}
	// Stands in for the registry-backed predicate: headless AND a harness
	// with a stream path.
	canStream := func(r ManifestRole) bool {
		switch r.Runtime.Image {
		case "claude", "codex", "opencode":
			return r.Runtime.Mode == RuntimeModeHeadless
		default:
			return false
		}
	}
	tests := []struct {
		name string
		team ManifestTeam
		// noPredicate passes nil where the daemon passes the registry-backed
		// predicate, which is a wiring error rather than a manifest problem.
		noPredicate bool
		wantErr     string
		wantsTeam   bool
	}{
		{
			name:      "a token ceiling with no headless role is refused",
			team:      ManifestTeam{Name: "crew", Budget: &ManifestBudget{MaxTokens: 10}, Roles: []ManifestRole{role("crew", RuntimeModeInteractive)}},
			wantErr:   "budget.max_tokens is declared but no role runs a stream-capable harness in headless mode",
			wantsTeam: true,
		},
		{
			// The hole a mode-only check left: generic implements no stream
			// path, so OBSERVED can never move off zero and the ceiling can
			// never refuse.
			name: "a headless role whose harness cannot stream is refused",
			team: ManifestTeam{Name: "crew", Budget: &ManifestBudget{MaxTokens: 10}, Roles: []ManifestRole{
				{Name: "crew", Replicas: 1, Runtime: ManifestRuntime{Image: "generic", Command: "sleep", Mode: RuntimeModeHeadless}},
			}},
			wantErr:   "no role runs a stream-capable harness in headless mode",
			wantsTeam: true,
		},
		{
			name: "a mixed team is allowed; partiality carries the honesty at runtime",
			team: ManifestTeam{Name: "crew", Budget: &ManifestBudget{MaxTokens: 10}, Roles: []ManifestRole{
				role("crew", RuntimeModeInteractive),
				role("reviewer", RuntimeModeHeadless),
			}},
		},
		{
			name: "a session ceiling depends on no harness",
			team: ManifestTeam{Name: "crew", Budget: &ManifestBudget{MaxSessions: 6}, Roles: []ManifestRole{role("crew", RuntimeModeInteractive)}},
		},
		{
			name: "no budget declares nothing to pre-flight",
			team: ManifestTeam{Name: "crew", Roles: []ManifestRole{role("crew", RuntimeModeInteractive)}},
		},
		{
			// A nil predicate is a wiring error, reported as one. Falling back
			// to the mode-only check would restore the silent hole.
			name:        "a missing predicate is an error, not a fallback",
			team:        ManifestTeam{Name: "crew", Budget: &ManifestBudget{MaxTokens: 10}, Roles: []ManifestRole{role("crew", RuntimeModeHeadless)}},
			noPredicate: true,
			wantErr:     "no stream-capability predicate supplied",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := &Manifest{Workspace: ManifestWorkspace{Name: "fanout"}, Teams: []ManifestTeam{tt.team}}
			pred := canStream
			if tt.noPredicate {
				pred = nil
			}
			err := m.ValidateBudgets(pred)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
			if tt.wantsTeam && !strings.Contains(err.Error(), "team[0=crew]") {
				t.Errorf("error = %q, want it to name the team", err)
			}
		})
	}
}

func TestValidateRuntimesOK(t *testing.T) {
	t.Parallel()
	// Any two binaries guaranteed on POSIX test hosts.
	m := &Manifest{
		Workspace: ManifestWorkspace{Name: "ok"},
		Teams: []ManifestTeam{{
			Name: "squad",
			Roles: []ManifestRole{
				{Name: "a", Replicas: 1, Runtime: ManifestRuntime{Command: "sh"}},
				{Name: "b", Replicas: 1, Runtime: ManifestRuntime{Command: "/bin/sh"}},
			},
		}},
	}
	if err := m.ValidateRuntimes(); err != nil {
		t.Fatalf("expected OK, got %v", err)
	}
}

func TestValidateRuntimesMissing(t *testing.T) {
	t.Parallel()
	m := &Manifest{
		Workspace: ManifestWorkspace{Name: "missing"},
		Teams: []ManifestTeam{{
			Name: "squad",
			Roles: []ManifestRole{
				{Name: "a", Replicas: 1, Runtime: ManifestRuntime{Command: "sh"}},
				{Name: "b", Replicas: 1, Runtime: ManifestRuntime{Command: "no-such-binary-marvel-9xyz"}},
				{Name: "c", Replicas: 1, Runtime: ManifestRuntime{Command: "/nope/not/here"}},
				// Relative path, also missing.
				{Name: "d", Replicas: 1, Runtime: ManifestRuntime{Command: "bin/nothing-here"}},
			},
		}},
	}
	err := m.ValidateRuntimes()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	// Every missing role should be named in the error so operators see
	// them all at once rather than one round-trip per problem.
	for _, want := range []string{"role[1=b]", "role[2=c]", "role[3=d]", "runtime pre-flight failed on 3 role(s)"} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected error to contain %q, got:\n%s", want, msg)
		}
	}
	// Role a (present) must not appear.
	if strings.Contains(msg, "role[0=a]") {
		t.Errorf("role[0=a] has a valid command; should not be reported:\n%s", msg)
	}
}

func TestValidateRuntimesScriptMissing(t *testing.T) {
	t.Parallel()
	m := &Manifest{
		Workspace: ManifestWorkspace{Name: "script-missing"},
		Teams: []ManifestTeam{{
			Name: "squad",
			Roles: []ManifestRole{
				{
					Name: "a", Replicas: 1,
					Runtime: ManifestRuntime{Command: "sh", Script: "scripts/does-not-exist.lua"},
				},
			},
		}},
	}
	err := m.ValidateRuntimes()
	if err == nil {
		t.Fatal("expected error on missing script, got nil")
	}
	if !strings.Contains(err.Error(), "script") {
		t.Errorf("expected error to mention script, got: %v", err)
	}
}

func TestValidateRuntimesEmptyCommand(t *testing.T) {
	t.Parallel()
	m := &Manifest{
		Workspace: ManifestWorkspace{Name: "empty"},
		Teams: []ManifestTeam{{
			Name: "squad",
			Roles: []ManifestRole{
				{Name: "a", Replicas: 1, Runtime: ManifestRuntime{Command: ""}},
			},
		}},
	}
	if err := m.ValidateRuntimes(); err == nil {
		t.Fatal("expected error on empty command")
	}
}

// TestValidateManifestPermissionModes covers aae-orc-6spa: every canonical
// --permission-mode value must parse, an out-of-set value must be rejected
// with the full allowlist in the message, and an empty value must stay
// valid (meaning "unset — adapter default").
func TestValidateManifestPermissionModes(t *testing.T) {
	t.Parallel()

	build := func(perm string) *Manifest {
		return &Manifest{
			Workspace: ManifestWorkspace{Name: "perm-test"},
			Teams: []ManifestTeam{{
				Name: "squad",
				Roles: []ManifestRole{{
					Name:        "worker",
					Replicas:    1,
					Permissions: perm,
					Runtime:     ManifestRuntime{Command: "bash"},
				}},
			}},
		}
	}

	valid := []string{"acceptEdits", "auto", "bypassPermissions", "default", "dontAsk", "plan"}
	for _, mode := range valid {
		mode := mode
		t.Run("valid/"+mode, func(t *testing.T) {
			t.Parallel()
			if _, err := validateManifest(build(mode)); err != nil {
				t.Fatalf("mode %q should be valid, got error: %v", mode, err)
			}
		})
	}

	t.Run("empty-is-valid", func(t *testing.T) {
		t.Parallel()
		if _, err := validateManifest(build("")); err != nil {
			t.Fatalf("empty permissions should be valid (unset), got error: %v", err)
		}
	})

	t.Run("invalid-rejected-with-allowlist", func(t *testing.T) {
		t.Parallel()
		_, err := validateManifest(build("hammertime"))
		if err == nil {
			t.Fatal("expected error on invalid permission mode")
		}
		msg := err.Error()
		if !strings.Contains(msg, "hammertime") {
			t.Errorf("error should name the bad value, got: %v", err)
		}
		for _, mode := range valid {
			if !strings.Contains(msg, mode) {
				t.Errorf("error message must list valid mode %q, got: %v", mode, err)
			}
		}
	})

	// dangerous_permissions is orthogonal: it must combine with any mode
	// and must not turn a valid mode into an error.
	t.Run("dangerous-permissions-combines-with-mode", func(t *testing.T) {
		t.Parallel()
		m := build("plan")
		m.Teams[0].Roles[0].DangerousPermissions = true
		if _, err := validateManifest(m); err != nil {
			t.Fatalf("dangerous_permissions with a valid mode should validate, got: %v", err)
		}
	})
}
