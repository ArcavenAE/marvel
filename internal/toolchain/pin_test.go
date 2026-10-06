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
	"errors"
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
// without any leading "v". The version is carried in a GOFUMPT_VERSION step
// env var (a KEY: value line renovate's shared customManager can bump), and
// the install step interpolates it: `go install mvdan.cc/gofumpt@${GOFUMPT_VERSION}`.
// It is read from the structured env field, the same shape as the
// golangci-lint `version:` input.
func ciGofumptVersion(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Env map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	const envKey = "GOFUMPT_VERSION"
	for _, job := range wf.Jobs {
		for _, step := range job.Steps {
			version, ok := step.Env[envKey]
			if !ok {
				continue
			}
			if version == "" {
				t.Fatalf("%s sets %s to an empty value", path, envKey)
			}
			return strings.TrimPrefix(version, "v")
		}
	}
	t.Fatalf("%s has no step setting %s", path, envKey)
	return ""
}

// TestNatsServerPinMatchesCI keeps the broker CI installs the one mise.toml
// pins and the fleet is verified against. The broker-backed internal/bus tests
// skip when nats-server is not on PATH, so a CI that does not install it
// quietly skips them; a CI that installs a different version tests another
// broker than the one deployed.
func TestNatsServerPinMatchesCI(t *testing.T) {
	t.Parallel()

	repo := filepath.Join("..", "..")
	pinned := misePin(t, filepath.Join(repo, "mise.toml"), "github:nats-io/nats-server")
	installed := ciEnvValue(t, filepath.Join(repo, ".github", "workflows", "ci.yml"), "NATS_SERVER_VERSION")

	if pinned != installed {
		t.Errorf("nats-server pin drift: mise.toml pins %q, ci.yml installs %q", pinned, installed)
	}
}

// TestNatsServerInstallIsVerifiedByChecksum requires the CI install to carry
// the release asset's SHA-256 and to check the download against it, so a
// changed or substituted download fails the job instead of running.
func TestNatsServerInstallIsVerifiedByChecksum(t *testing.T) {
	t.Parallel()

	step, err := ciEnvStep(filepath.Join("..", "..", ".github", "workflows", "ci.yml"), "NATS_SERVER_SHA256")
	if err != nil {
		t.Fatal(err)
	}
	if len(step.value) != 64 || strings.Trim(step.value, "0123456789abcdef") != "" {
		t.Errorf("NATS_SERVER_SHA256 = %q, want 64 lowercase hex digits", step.value)
	}
	if !checksumChecked(step.run, "NATS_SERVER_SHA256") {
		t.Errorf("the install step sets NATS_SERVER_SHA256 but never checks the download against it:\n%s", step.run)
	}
}

func TestChecksumChecked(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, run string
		want      bool
	}{
		{"checked strictly", `echo "${NATS_SERVER_SHA256}  ${f}" | sha256sum --check --strict -`, true},
		{"checked", `echo "${NATS_SERVER_SHA256}  f" | sha256sum --check -`, true},
		{"line deleted", "curl -o f url\ntar -xzf f", false},
		{"sum named but not checked", `echo "${NATS_SERVER_SHA256}"`, false},
		{"check of another variable", `echo "${OTHER}  f" | sha256sum --check -`, false},
		{"checker without the sum", "sha256sum f", false},
	} {
		if got := checksumChecked(tc.run, "NATS_SERVER_SHA256"); got != tc.want {
			t.Errorf("%s: checksumChecked = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCIEnvStepRefusesTwoJobsThatDisagree(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "ci.yml")
	write := func(a, b string) {
		t.Helper()
		doc := "jobs:\n  one:\n    steps:\n      - env:\n          K: " + a + "\n  two:\n    steps:\n      - env:\n          K: " + b + "\n"
		if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("v1", "v2")
	if _, err := ciEnvStep(path, "K"); err == nil {
		t.Error("two jobs setting K to different values read without error")
	}
	write("v1", "v1")
	step, err := ciEnvStep(path, "K")
	if err != nil || step.value != "v1" {
		t.Errorf("two jobs agreeing on K = %+v, %v, want v1", step, err)
	}
}

type ciStep struct {
	value string
	run   string
}

// ciEnvStep returns the value of a step env var in the CI workflow, without any
// leading "v", with the step's run script. It fails when no step sets it, when
// it is empty, or when two jobs set it to different values, so which job is
// read never depends on map order.
func ciEnvStep(path, key string) (ciStep, error) {
	return ciStep{}, errors.New("not implemented")
}

// checksumChecked reports whether a run script checks a download against the
// SHA-256 held in the named env var.
func checksumChecked(run, envVar string) bool {
	return false
}

// ciEnvValue returns the value of a step env var in the CI workflow.
func ciEnvValue(t *testing.T, path, key string) string {
	t.Helper()
	step, err := ciEnvStep(path, key)
	if err != nil {
		t.Fatal(err)
	}
	return step.value
}
