// Package toolchain carries no source of its own. It exists for the test
// below, which keeps the repo's pinned lint toolchain and the version CI
// installs from drifting apart.
//
// `just lint` and lefthook's pre-commit lint step both invoke a bare
// `golangci-lint`, so without a repo-level pin the name resolves against
// ambient machine state: absent on a fresh machine, and some other version
// on a machine that happens to have one. CI is unaffected because it
// installs its own copy, which is exactly why the gap stayed invisible.
// See issue #119.
package toolchain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

const lintTool = "golangci-lint"

func TestGolangciLintPinMatchesCI(t *testing.T) {
	t.Parallel()

	repo := filepath.Join("..", "..")
	pinned := misePin(t, filepath.Join(repo, "mise.toml"), lintTool)
	installed := ciLintVersion(t, filepath.Join(repo, ".github", "workflows", "ci.yml"))

	if pinned != installed {
		t.Errorf("%s pin drift: mise.toml pins %q, ci.yml installs %q", lintTool, pinned, installed)
	}
}

// misePin returns the version a mise config pins for tool, without any
// leading "v". The tool value is decoded as any because mise accepts
// either a bare version string or a per-tool table; only the string form
// is a pin this test can compare.
func misePin(t *testing.T, path, tool string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var cfg struct {
		Tools map[string]any `toml:"tools"`
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	raw, ok := cfg.Tools[tool]
	if !ok {
		t.Fatalf("%s does not pin %s; local lint resolves against ambient machine state", path, tool)
	}
	version, ok := raw.(string)
	if !ok {
		t.Fatalf("%s pins %s as %T, want a version string", path, tool, raw)
	}
	return strings.TrimPrefix(version, "v")
}

// ciLintVersion returns the golangci-lint version the CI workflow installs,
// without any leading "v".
func ciLintVersion(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Uses string `yaml:"uses"`
				With struct {
					Version string `yaml:"version"`
				} `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, job := range wf.Jobs {
		for _, step := range job.Steps {
			if !strings.Contains(step.Uses, lintTool+"-action") {
				continue
			}
			if step.With.Version == "" {
				t.Fatalf("%s runs %s-action without a pinned version", path, lintTool)
			}
			return strings.TrimPrefix(step.With.Version, "v")
		}
	}
	t.Fatalf("%s has no %s-action step", path, lintTool)
	return ""
}
