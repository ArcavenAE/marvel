package api

import (
	"fmt"
	"strings"
	"testing"
)

const labelManifestTOML = `
[workspace]
name = "test"

[[team]]
name = "squad"
%s

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    image = "forestage"
    command = "forestage"
    %s
`

func labelManifest(teamLine, roleLine string) []byte {
	return []byte(fmt.Sprintf(labelManifestTOML, teamLine, roleLine))
}

func appliedRuntimeLabel(t *testing.T, data []byte) string {
	t.Helper()
	m, err := ParseManifestBytes(data)
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
	return team.Roles[0].Runtime.AccountLabel
}

// A team label fills a role that declares none (svccol-2, X1).
func TestAccountLabelTeamDefaultFillsRole(t *testing.T) {
	t.Parallel()
	if got := appliedRuntimeLabel(t, labelManifest(`account_label = "ultra"`, "")); got != "ultra" {
		t.Errorf("role runtime label = %q, want the team's ultra", got)
	}
}

// A role's own label wins over the team's.
func TestAccountLabelRoleWins(t *testing.T) {
	t.Parallel()
	if got := appliedRuntimeLabel(t, labelManifest(`account_label = "ultra"`, `account_label = "enterprise"`)); got != "enterprise" {
		t.Errorf("role runtime label = %q, want the role's enterprise", got)
	}
	if got := appliedRuntimeLabel(t, labelManifest("", `account_label = "enterprise"`)); got != "enterprise" {
		t.Errorf("role runtime label = %q, want enterprise with no team label", got)
	}
	if got := appliedRuntimeLabel(t, labelManifest("", "")); got != "" {
		t.Errorf("role runtime label = %q, want none declared", got)
	}
}

// The label is a short word the operator declares. Each refusal names where it
// is and the field, and never echoes the label.
func TestAccountLabelRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		label string
		ok    bool
		quote string
	}{
		{"32 runes", strings.Repeat("a", 32), true, ""},
		{"33 runes", strings.Repeat("a", 33), false, ""},
		{"escape", "ul\x1b[31m", false, ""},
		{"right-to-left override", "ul\u202etra", false, ""},
		{"no-break space", "ul tra", false, ""},
		{"tab", "ul\ttra", false, ""},
		{"space", "ul tra", false, ""},
		{"at sign", "me@host", false, ""},
		{"replacement char", "ul�tra", false, ""},
		{"api key prefix", "sk-ant-x", false, "looks like an API key"},
		{"invalid utf-8", "ul\xfftra", false, ""},
		{"c1 control", "ul\u0085tra", false, ""},
		{"delete", "ul\x7ftra", false, ""},
		{"plain word", "alt-ultra", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAccountLabel("team squad", tc.label)
			if tc.ok {
				if err != nil {
					t.Fatalf("label %q refused: %v", tc.label, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("label %q was accepted", tc.label)
			}
			msg := err.Error()
			if !strings.Contains(msg, "team squad") || !strings.Contains(msg, "account_label") {
				t.Errorf("error %q does not name the team and the field", msg)
			}
			if tc.label != "" && strings.Contains(msg, tc.label) {
				t.Errorf("error %q echoes the label", msg)
			}
			if tc.quote != "" && !strings.Contains(msg, tc.quote) {
				t.Errorf("error %q lacks %q", msg, tc.quote)
			}
		})
	}
}

// A refused label stops the manifest at load, on the team and on a role, and
// the error names which.
func TestAccountLabelRefusedAtManifestLoad(t *testing.T) {
	t.Parallel()
	_, err := ParseManifestBytes(labelManifest(`account_label = "me@host"`, ""))
	if err == nil || !strings.Contains(err.Error(), "squad") || !strings.Contains(err.Error(), "account_label") || strings.Contains(err.Error(), "me@host") {
		t.Errorf("team label error = %v, want one naming the team and the field and not the label", err)
	}
	_, err = ParseManifestBytes(labelManifest("", `account_label = "me@host"`))
	if err == nil || !strings.Contains(err.Error(), "worker") || !strings.Contains(err.Error(), "account_label") || strings.Contains(err.Error(), "me@host") {
		t.Errorf("role label error = %v, want one naming the role and the field and not the label", err)
	}
}

// A label edit is drift of the runtime, named by field (information only).
func TestRuntimeDriftNamesAccountLabel(t *testing.T) {
	t.Parallel()
	a, b := fullRuntime(), fullRuntime()
	b.AccountLabel = "enterprise"
	got := RuntimeDrift(a, b)
	if len(got) != 1 || got[0] != "account_label" {
		t.Errorf("drift = %v, want [account_label]", got)
	}
}

// The label is display text and never part of the account's identity: two
// sessions that differ only by label are one account.
func TestAccountKeyIgnoresLabel(t *testing.T) {
	t.Parallel()
	a := Session{Runtime: Runtime{Name: "claude", AccountLabel: "ultra"}, AccountHome: "/h/x", BackendResolved: BackendDefaultName}
	b := a
	b.Runtime.AccountLabel = "enterprise"
	if AccountKeyOf(a) != AccountKeyOf(b) {
		t.Errorf("keys differ by label: %v and %v", AccountKeyOf(a), AccountKeyOf(b))
	}
}
