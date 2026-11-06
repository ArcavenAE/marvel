package runtime

import (
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/arcavenae/marvel/internal/api"
)

// seededGooseConfig seeds a goose launch's private home and reads config.yaml
// back.
func seededGooseConfig(t *testing.T, ctx *LaunchContext) map[string]any {
	t.Helper()
	spec, ok := (&Goose{}).SessionHome(ctx)
	if !ok || spec.Seed == nil {
		t.Fatalf("SessionHome = %+v, %v", spec, ok)
	}
	dir := t.TempDir()
	if err := spec.Seed(dir); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(readFile(t, filepath.Join(dir, "config", "config.yaml")), &cfg); err != nil {
		t.Fatalf("config.yaml: %v", err)
	}
	return cfg
}

// A declared model reaches goose as GOOSE_MODEL in the launch env and in the
// seeded config, and adds nothing to the command line.
func TestGoosePrepareRendersModelAsEnv(t *testing.T) {
	t.Parallel()
	with := gooseContext(t, func(rt *api.Runtime) { rt.Model = "qwen3:8b" })
	without := gooseContext(t, nil)
	res, err := (&Goose{}).Prepare(with)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got := res.Env["GOOSE_MODEL"]; got != "qwen3:8b" {
		t.Errorf("GOOSE_MODEL in the launch env = %q, want qwen3:8b", got)
	}
	if cfg := seededGooseConfig(t, with); cfg["GOOSE_MODEL"] != "qwen3:8b" {
		t.Errorf("seeded config GOOSE_MODEL = %v, want qwen3:8b", cfg["GOOSE_MODEL"])
	}
	base, err := (&Goose{}).Prepare(without)
	if err != nil {
		t.Fatalf("Prepare without a model: %v", err)
	}
	// The stderr file differs by launch, so compare the command up to it.
	cut := func(c string) string { return strings.SplitN(c, " 2> ", 2)[0] }
	if cut(res.Command) != cut(base.Command) || strings.Contains(res.Command, "model") {
		t.Errorf("command with a model = %q, without = %q, want the same command", res.Command, base.Command)
	}
	if _, ok := base.Env["GOOSE_MODEL"]; ok {
		t.Error("a launch with no model set GOOSE_MODEL")
	}
}

// The context cap and the seeded config read the declared model, not only
// runtime.env: qwen3:0.6b has a 4096 entry in the ceiling table.
func TestGooseContextLimitFromRuntimeModel(t *testing.T) {
	t.Parallel()
	ctx := gooseContext(t, func(rt *api.Runtime) { rt.Model = "qwen3:0.6b" })
	res, err := (&Goose{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got := res.Env["GOOSE_CONTEXT_LIMIT"]; got != "4096" {
		t.Errorf("GOOSE_CONTEXT_LIMIT = %q, want 4096", got)
	}
	if cfg := seededGooseConfig(t, ctx); cfg["GOOSE_MODEL"] != "qwen3:0.6b" {
		t.Errorf("seeded config GOOSE_MODEL = %v, want qwen3:0.6b", cfg["GOOSE_MODEL"])
	}
}
