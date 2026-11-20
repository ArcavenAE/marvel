package api

import (
	"strings"
	"testing"
)

// runtime.first_run_answers = false turns off marvel's answers to the harness's
// first-run and setup screens for a role; unset leaves them on.

const firstRunAnswersManifest = `
[workspace]
name = "acme"

[[team]]
name = "squad"

  [[team.role]]
  name = "on-by-default"
  replicas = 1
  [team.role.runtime]
  image = "claude"
  command = "claude"

  [[team.role]]
  name = "opted-out"
  replicas = 1
  [team.role.runtime]
  image = "claude"
  command = "claude"
  first_run_answers = false
`

func TestFirstRunAnswersIsCarriedOnApply(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(firstRunAnswersManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	team, err := store.GetTeam("acme/squad")
	if err != nil {
		t.Fatal(err)
	}
	on := map[string]bool{}
	for _, r := range team.Roles {
		on[r.Name] = r.Runtime.FirstRunAnswersOn()
	}
	if !on["on-by-default"] {
		t.Errorf("a role that declares nothing has first-run answers off")
	}
	if on["opted-out"] {
		t.Errorf("first_run_answers = false was not carried to the role's runtime")
	}
}

func TestFirstRunAnswersDefaultsOn(t *testing.T) {
	t.Parallel()
	if !(Runtime{}).FirstRunAnswersOn() {
		t.Error("the zero Runtime has first-run answers off, want on")
	}
	off, on := false, true
	if (Runtime{FirstRunAnswers: &off}).FirstRunAnswersOn() || !(Runtime{FirstRunAnswers: &on}).FirstRunAnswersOn() {
		t.Error("an explicit first_run_answers value is not honoured")
	}
}

// A value that is not a bool is refused at parse, in either manifest format.
func TestFirstRunAnswersRefusesANonBool(t *testing.T) {
	t.Parallel()
	toml := strings.Replace(firstRunAnswersManifest, "first_run_answers = false", `first_run_answers = "no"`, 1)
	if _, err := ParseManifestBytes([]byte(toml)); err == nil {
		t.Error("a TOML manifest with first_run_answers = \"no\" parsed")
	}
	yaml := `
workspace:
  name: acme
teams:
  - name: squad
    roles:
      - name: seat
        replicas: 1
        runtime:
          image: claude
          command: claude
          first_run_answers: maybe
`
	if _, err := ParseManifestBytes([]byte(yaml)); err == nil {
		t.Error("a YAML manifest with first_run_answers: maybe parsed")
	}
}
