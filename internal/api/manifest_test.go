package api

import (
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
