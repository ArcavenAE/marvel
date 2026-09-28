package api

import (
	"strings"
	"testing"
)

// marvel#335: replicas 0 is legal desired state ("parked"), the same bound
// marvel scale already applies, so a parked role can be written down.

func TestParseManifestReplicasZeroIsParkedTOML(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(`
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
	if err != nil {
		t.Fatalf("replicas = 0 should parse as a parked role, got: %v", err)
	}
	if got := m.Teams[0].Roles[0].Replicas; got != 0 {
		t.Fatalf("replicas = %d, want 0", got)
	}
}

func TestParseManifestReplicasZeroIsParkedYAML(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(`
workspace:
  name: test
teams:
  - name: agents
    roles:
      - name: worker
        replicas: 0
        runtime:
          command: bash
`))
	if err != nil {
		t.Fatalf("replicas: 0 should parse as a parked role, got: %v", err)
	}
	if got := m.Teams[0].Roles[0].Replicas; got != 0 {
		t.Fatalf("replicas = %d, want 0", got)
	}
}

// An omitted replicas must not become a silently parked role: before this
// change it was refused as 0, and it stays refused, with a message that
// says 0 is how to park.
func TestParseManifestReplicasOmittedIsRefused(t *testing.T) {
	t.Parallel()
	for name, doc := range map[string]string{
		"toml": `
[workspace]
name = "test"

[[team]]
name = "agents"

  [[team.role]]
  name = "worker"

    [team.role.runtime]
    command = "bash"
`,
		"yaml": `
workspace:
  name: test
teams:
  - name: agents
    roles:
      - name: worker
        runtime:
          command: bash
`,
	} {
		name, doc := name, doc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseManifestBytes([]byte(doc))
			if err == nil {
				t.Fatal("an omitted replicas should be refused, got no error")
			}
			for _, want := range []string{`team "agents" role "worker"`, "replicas is required"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

// A negative count is refused by the same validator marvel scale uses, and
// the error names the team and role instead of team[0].role[0].
func TestParseManifestNegativeReplicasNamesTeamAndRole(t *testing.T) {
	t.Parallel()
	_, err := ParseManifestBytes([]byte(`
[workspace]
name = "test"

[[team]]
name = "agents"

  [[team.role]]
  name = "worker"
  replicas = -1

    [team.role.runtime]
    command = "bash"
`))
	if err == nil {
		t.Fatal("replicas = -1 should be refused")
	}
	want := ValidateReplicas("agents", "worker", -1).Error()
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not carry the scale validator's message %q", err, want)
	}
}
