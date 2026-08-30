// Package toolchain carries no source of its own. It exists for the tests
// below, which keep the repo's pinned dev toolchain and the versions CI
// installs from drifting apart.
//
// `just lint` and lefthook's pre-commit lint step both invoke a bare
// `golangci-lint`; `just fmt` and lefthook's fmt-check both invoke a bare
// `gofumpt`. Without a repo-level pin those names resolve against ambient
// machine state: absent on a fresh machine, and some other version on a
// machine that happens to have one. CI is unaffected because it installs
// its own copies, which is exactly why the gap stayed invisible. See
// issue #119 (golangci-lint) and the gofumpt follow-on.
package toolchain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

const (
	lintTool = "golangci-lint"
	fmtTool  = "gofumpt"
)

func TestGolangciLintPinMatchesCI(t *testing.T) {
	t.Parallel()

	repo := filepath.Join("..", "..")
	pinned := misePin(t, filepath.Join(repo, "mise.toml"), lintTool)
	installed := ciLintVersion(t, filepath.Join(repo, ".github", "workflows", "ci.yml"))

	if pinned != installed {
		t.Errorf("%s pin drift: mise.toml pins %q, ci.yml installs %q", lintTool, pinned, installed)
	}
}

func TestGofumptPinMatchesCI(t *testing.T) {
	t.Parallel()

	repo := filepath.Join("..", "..")
	pinned := misePin(t, filepath.Join(repo, "mise.toml"), fmtTool)
	installed := ciGofumptVersion(t, filepath.Join(repo, ".github", "workflows", "ci.yml"))

	if pinned != installed {
		t.Errorf("%s pin drift: mise.toml pins %q, ci.yml installs %q", fmtTool, pinned, installed)
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

// ciGofumptVersion returns the gofumpt version the CI workflow installs,
// without any leading "v". Unlike golangci-lint (a pinned action with a
// `version:` input), gofumpt is installed by a plain run step,
// `go install mvdan.cc/gofumpt@<version>`, so the version is parsed out of
// the step's command rather than a structured field.
func ciGofumptVersion(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	const marker = "mvdan.cc/gofumpt@"
	for _, job := range wf.Jobs {
		for _, step := range job.Steps {
			idx := strings.Index(step.Run, marker)
			if idx < 0 {
				continue
			}
			fields := strings.Fields(step.Run[idx+len(marker):])
			if len(fields) == 0 {
				t.Fatalf("%s installs gofumpt without a version", path)
			}
			return strings.TrimPrefix(fields[0], "v")
		}
	}
	t.Fatalf("%s has no `go install %s<version>` step", path, marker)
	return ""
}
