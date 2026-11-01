package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/arcavenae/marvel/internal/api"
)

// gooseManageExtensionsTool is the tool every seat has under never_allow: a seat
// that can add extensions can add a shell, so the permission file never leaves
// it out (goose-adapter-v1.md section 3, v1.1 fact L6).
const gooseManageExtensionsTool = "extensionmanager__manage_extensions"

// gooseConfigFile and goosePermissionFile are goose's own names under its config
// directory, which GOOSE_PATH_ROOT puts at <root>/config.
const (
	gooseConfigDir      = "config"
	gooseConfigFile     = "config.yaml"
	goosePermissionFile = "permission.yaml"
)

// goosePermission is permission.yaml as goose reads it at v1.57.0: a map from a
// permission category to three lists, every list required
// (crates/goose/src/config/permission.rs, PermissionConfig). approve reads the
// "user" category.
type goosePermission struct {
	AlwaysAllow []string `yaml:"always_allow"`
	AskBefore   []string `yaml:"ask_before"`
	NeverAllow  []string `yaml:"never_allow"`
}

// gooseExtensionEntry is a builtin extension entry in config.yaml, the smallest
// form goose accepts (ExtensionEntry over ExtensionConfig::Builtin, v1.57.0).
type gooseExtensionEntry struct {
	Enabled bool   `yaml:"enabled"`
	Type    string `yaml:"type"`
	Name    string `yaml:"name"`
}

// gooseSeeder returns the Seed step for one launch: config.yaml with the mode,
// the provider and model the role's env names, and the extensions; and
// permission.yaml with the role's tools. Neither carries a credential: the
// provider key stays where goose finds it (v1 section 6).
func gooseSeeder(ctx *LaunchContext) func(dir string) error {
	if ctx == nil || ctx.Session == nil {
		return nil
	}
	rt := ctx.Session.Runtime
	return func(dir string) error {
		g := rt.Goose
		if g == nil || g.Posture == "" {
			// A seat with no posture is refused, so it needs no seed.
			return nil
		}
		cfgDir := filepath.Join(dir, gooseConfigDir)
		if err := os.MkdirAll(cfgDir, 0o700); err != nil {
			return fmt.Errorf("make goose config dir: %w", err)
		}
		cfg := map[string]any{gooseModeEnv: g.Posture}
		for _, k := range []string{"GOOSE_PROVIDER", "GOOSE_MODEL"} {
			if v := strings.TrimSpace(rt.Env[k]); v != "" {
				cfg[k] = v
			}
		}
		if len(g.Extensions) > 0 {
			exts := make(map[string]gooseExtensionEntry, len(g.Extensions))
			for _, name := range g.Extensions {
				exts[name] = gooseExtensionEntry{Enabled: true, Type: "builtin", Name: name}
			}
			cfg["extensions"] = exts
		}
		perm := map[string]goosePermission{"user": {
			AlwaysAllow: dedupe(g.ToolsAllow),
			AskBefore:   []string{},
			NeverAllow:  dedupe(append(slices.Clone(g.ToolsDeny), gooseManageExtensionsTool)),
		}}
		if err := writeGooseYAML(filepath.Join(cfgDir, gooseConfigFile), cfg); err != nil {
			return err
		}
		return writeGooseYAML(filepath.Join(cfgDir, goosePermissionFile), perm)
	}
}

// dedupe keeps the first of each entry, in order, and returns an empty list
// rather than nil so the YAML key is a list, which goose requires.
func dedupe(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func writeGooseYAML(path string, v any) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", filepath.Base(path), err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// gooseBuiltins is the --with-builtin argument for the role's extensions, or ""
// for none.
func gooseBuiltins(g *api.GooseRuntime) string {
	if g == nil {
		return ""
	}
	return strings.Join(g.Extensions, ",")
}
