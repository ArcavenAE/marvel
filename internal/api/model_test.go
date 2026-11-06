package api

import (
	"strings"
	"testing"
)

// runtime.model: parse and carry, then the apply-time refusals
// (docs/design/role-runtime-model.md). The refusals are asked of a fake
// predicate here; the daemon wires the registry-backed one.

const modelYAML = `
workspace:
  name: ws
teams:
  - name: crew
    roles:
      - name: w
        replicas: 1
        runtime:
          image: claude
          command: claude
          model: sonnet
`

const modelTOML = `
[workspace]
name = "ws"

[[team]]
name = "crew"

  [[team.role]]
  name = "w"
  replicas = 1

    [team.role.runtime]
    image = "claude"
    command = "claude"
    model = "sonnet"
`

func TestManifestRuntimeModelParsesTOMLAndYAML(t *testing.T) {
	t.Parallel()
	for name, src := range map[string]string{"yaml": modelYAML, "toml": modelTOML} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m, err := ParseManifestBytes([]byte(src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := m.Teams[0].Roles[0].Runtime.Model; got != "sonnet" {
				t.Fatalf("parsed Model = %q, want sonnet", got)
			}
			store := NewStore()
			if err := m.Apply(store); err != nil {
				t.Fatalf("apply: %v", err)
			}
			team, err := store.GetTeam("ws/crew")
			if err != nil {
				t.Fatalf("get team: %v", err)
			}
			if got := team.Roles[0].Runtime.Model; got != "sonnet" {
				t.Errorf("stored Runtime.Model = %q, want sonnet", got)
			}
		})
	}
}

func TestManifestRuntimeModelAbsentIsEmpty(t *testing.T) {
	t.Parallel()
	src := strings.Replace(modelYAML, "          model: sonnet\n", "", 1)
	m, err := ParseManifestBytes([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	team, _ := store.GetTeam("ws/crew")
	if got := team.Roles[0].Runtime.Model; got != "" {
		t.Errorf("Runtime.Model = %q with no model declared, want empty", got)
	}
}

func TestManifestRuntimeModelShapeRefusals(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"blank after trim": `"   "`,
		"inner space":      `"claude sonnet"`,
		"tab":              `"claude\tsonnet"`,
		"leading dash":     `"-sonnet"`,
		"257 bytes":        `"` + strings.Repeat("a", 257) + `"`,
		"control byte":     `"claude\u0001"`,
		"non-ascii":        `"claude-sonnét"`,
	}
	for name, quoted := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src := strings.Replace(modelYAML, "model: sonnet", "model: "+quoted, 1)
			_, err := ParseManifestBytes([]byte(src))
			if err == nil {
				t.Fatalf("model %s was accepted", quoted)
			}
			if !strings.Contains(err.Error(), "team[0].role[0].runtime.model") {
				t.Errorf("error %q does not name team[0].role[0].runtime.model", err)
			}
		})
	}
}

func TestRuntimeDriftNamesModel(t *testing.T) {
	t.Parallel()
	a, b := fullRuntime(), fullRuntime()
	b.Model = "claude-opus-5"
	got := RuntimeDrift(a, b)
	if len(got) != 1 || got[0] != "model" {
		t.Errorf("drift = %v, want [model]", got)
	}
}

// canRenderFake stands in for the registry-backed predicate: the harnesses
// that can be handed a model.
func canRenderFake(r ManifestRole) bool {
	switch r.Runtime.AdapterName() {
	case "claude", "codex", "opencode", "goose":
		return true
	}
	return false
}

func modelRole(image, command, model string, args []string, env map[string]string) *Manifest {
	return &Manifest{Teams: []ManifestTeam{{Name: "crew", Roles: []ManifestRole{{
		Name: "w", Replicas: 1,
		Runtime: ManifestRuntime{Image: image, Command: command, Model: model, Args: args, Env: env},
	}}}}}
}

func TestValidateModelsRefusesAdapterWithoutModel(t *testing.T) {
	t.Parallel()
	for _, image := range []string{"generic", "simulator", "forestage"} {
		t.Run(image, func(t *testing.T) {
			t.Parallel()
			err := modelRole(image, image, "sonnet", nil, nil).ValidateModels(canRenderFake)
			if err == nil {
				t.Fatalf("a model on %s was accepted", image)
			}
			for _, want := range []string{"crew", "w", image} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
		})
	}
}

func TestValidateModelsNilPredicateIsWiringError(t *testing.T) {
	t.Parallel()
	if err := modelRole("claude", "claude", "sonnet", nil, nil).ValidateModels(nil); err == nil {
		t.Error("a nil predicate with a declared model was accepted")
	}
	if err := modelRole("claude", "claude", "", nil, nil).ValidateModels(nil); err != nil {
		t.Errorf("a nil predicate with no declared model = %v, want nil", err)
	}
}

func TestValidateModelsRefusesModelFlagInArgs(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"--model":   {"--model", "x"},
		"--model=x": {"--model=x"},
		"-m":        {"-m", "x"},
		"-m=x":      {"-m=x"},
		"dangling":  {"--model"},
	}
	for want, args := range cases {
		t.Run(want, func(t *testing.T) {
			t.Parallel()
			err := modelRole("claude", "claude", "sonnet", args, nil).ValidateModels(canRenderFake)
			if err == nil {
				t.Fatalf("a model beside args %v was accepted", args)
			}
			spelling := strings.SplitN(want, "=", 2)[0]
			if want == "dangling" {
				spelling = "--model"
			}
			if !strings.Contains(err.Error(), spelling) {
				t.Errorf("error %q does not name %q", err, spelling)
			}
		})
	}
}

func TestValidateModelsRefusesModelFlagInCommandWords(t *testing.T) {
	t.Parallel()
	err := modelRole("claude", "claude --model opus", "sonnet", nil, nil).ValidateModels(canRenderFake)
	if err == nil {
		t.Fatal("a model beside a command carrying --model was accepted")
	}
	if !strings.Contains(err.Error(), "--model") {
		t.Errorf("error %q does not name --model", err)
	}
}

func TestValidateModelsRefusesAnthropicModelEnvOnClaude(t *testing.T) {
	t.Parallel()
	const secretLooking = "claude-opus-private-value"
	err := modelRole("claude", "claude", "sonnet", nil, map[string]string{"ANTHROPIC_MODEL": secretLooking}).ValidateModels(canRenderFake)
	if err == nil {
		t.Fatal("a model beside runtime.env.ANTHROPIC_MODEL was accepted")
	}
	if !strings.Contains(err.Error(), "ANTHROPIC_MODEL") {
		t.Errorf("error %q does not name the key", err)
	}
	if strings.Contains(err.Error(), secretLooking) {
		t.Errorf("error %q carries the env value", err)
	}
}

func TestValidateModelsRefusesGooseModelEnvOnGoose(t *testing.T) {
	t.Parallel()
	const secretLooking = "qwen-private-value"
	err := modelRole("goose", "goose", "qwen3:8b", nil, map[string]string{"GOOSE_MODEL": secretLooking}).ValidateModels(canRenderFake)
	if err == nil {
		t.Fatal("a model beside runtime.env.GOOSE_MODEL was accepted")
	}
	if !strings.Contains(err.Error(), "GOOSE_MODEL") {
		t.Errorf("error %q does not name the key", err)
	}
	if strings.Contains(err.Error(), secretLooking) {
		t.Errorf("error %q carries the env value", err)
	}
	if err := modelRole("goose", "goose", "", nil, map[string]string{"GOOSE_MODEL": "x"}).ValidateModels(canRenderFake); err != nil {
		t.Errorf("GOOSE_MODEL with no runtime.model = %v, want accepted", err)
	}
}

func TestValidateModelsEnvRuleIsPerAdapter(t *testing.T) {
	t.Parallel()
	err := modelRole("codex", "codex", "gpt-5", nil, map[string]string{"ANTHROPIC_MODEL": "x"}).ValidateModels(canRenderFake)
	if err != nil {
		t.Errorf("ANTHROPIC_MODEL on a codex role = %v, want it left alone by the env rule", err)
	}
}

func TestValidateModelsLegacyArgsUnchanged(t *testing.T) {
	t.Parallel()
	if err := modelRole("claude", "claude", "", []string{"--model", "x"}, nil).ValidateModels(canRenderFake); err != nil {
		t.Errorf("a role with no runtime.model and --model in args = %v, want accepted", err)
	}
}
