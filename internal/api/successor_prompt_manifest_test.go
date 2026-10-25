package api

import "testing"

// marvel#391, design D3: runtime.successor_prompt = false turns the successor
// first message off for a role; unset leaves it on.

const successorPromptManifest = `
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
  successor_prompt = false
`

func TestSuccessorPromptIsCarriedOnApply(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(successorPromptManifest))
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
		on[r.Name] = r.Runtime.SuccessorPromptOn()
	}
	if !on["on-by-default"] {
		t.Errorf("a role that declares nothing has the successor prompt off")
	}
	if on["opted-out"] {
		t.Errorf("successor_prompt = false was not carried to the role's runtime")
	}
}

func TestSuccessorPromptDefaultsOn(t *testing.T) {
	t.Parallel()
	if !(Runtime{}).SuccessorPromptOn() {
		t.Error("the zero Runtime has the successor prompt off, want on")
	}
	off, on := false, true
	if (Runtime{SuccessorPrompt: &off}).SuccessorPromptOn() || !(Runtime{SuccessorPrompt: &on}).SuccessorPromptOn() {
		t.Error("an explicit successor_prompt value is not honoured")
	}
}

func TestRuntimeDriftNamesSuccessorPrompt(t *testing.T) {
	t.Parallel()
	off := false
	drift := RuntimeDrift(Runtime{}, Runtime{SuccessorPrompt: &off})
	if len(drift) != 1 || drift[0] != "successor_prompt" {
		t.Errorf("drift = %v, want [successor_prompt]", drift)
	}
	if d := RuntimeDrift(Runtime{}, Runtime{}); len(d) != 0 {
		t.Errorf("identical runtimes drift: %v", d)
	}
}
