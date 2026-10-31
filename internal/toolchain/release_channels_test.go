package toolchain

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The alpha channel and the stable channel are built by two different
// pipelines, and both end by writing a formula into the same tap. A formula
// file written by both is overwritten by whichever runs last, so a host that
// installed one channel is moved to the other by `brew upgrade`. These tests
// hold the files apart, hold the stable build tool to one exact version, and
// hold every tap commit to a signed one.

// goreleaserConfig is the part of .goreleaser.yml the tests read.
type goreleaserConfig struct {
	ProjectName string   `yaml:"project_name"`
	Env         []string `yaml:"env"`
	Brews       []struct {
		Name         string `yaml:"name"`
		Directory    string `yaml:"directory"`
		SkipUpload   any    `yaml:"skip_upload"`
		CommitAuthor struct {
			Signing struct {
				Enabled bool `yaml:"enabled"`
			} `yaml:"signing"`
		} `yaml:"commit_author"`
	} `yaml:"brews"`
}

func readGoreleaser(t *testing.T) goreleaserConfig {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg goreleaserConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse .goreleaser.yml: %v", err)
	}
	return cfg
}

var envTemplate = regexp.MustCompile(`^\{\{\s*\.Env\.([A-Za-z0-9_]+)\s*\}\}$`)

// formulaFile is the tap file a brews entry writes: its name, or the project
// name when it sets none, with .rb. A name that is a template must be a bare
// .Env reference to a top-level env entry, which is how the stable name is kept
// in one place; any other template is not resolved here and fails the test.
func formulaFile(cfg goreleaserConfig, name string) (string, error) {
	if name == "" {
		name = cfg.ProjectName
	}
	if m := envTemplate.FindStringSubmatch(name); m != nil {
		for _, e := range cfg.Env {
			if k, v, ok := strings.Cut(e, "="); ok && k == m[1] {
				return v + ".rb", nil
			}
		}
		return "", fmt.Errorf("brews name %q names an env entry that .goreleaser.yml does not set", name)
	}
	if strings.Contains(name, "{{") {
		return "", fmt.Errorf("brews name %q is a template this test does not resolve", name)
	}
	return name + ".rb", nil
}

var (
	alphaCopy = regexp.MustCompile(`cp\s+\S+\s+tap-repo/Formula/(\S+)`)
	alphaAdd  = regexp.MustCompile(`git add\s+Formula/(\S+)`)
)

// alphaTapStep returns the ci.yml step that writes the alpha formula into the
// tap, and the file it copies and the file it stages.
func alphaTapStep(t *testing.T) (step ciJobStep, copied, staged string) {
	t.Helper()
	jobs, err := ciJobs(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var found []ciJobStep
	for _, job := range jobs {
		for _, s := range job.Steps {
			if strings.Contains(s.Run, "tap-repo/Formula/") {
				found = append(found, s)
			}
		}
	}
	if len(found) != 1 {
		t.Fatalf("ci.yml has %d steps that write into tap-repo/Formula, want exactly one", len(found))
	}
	step = found[0]
	if m := alphaCopy.FindStringSubmatch(step.Run); m != nil {
		copied = m[1]
	}
	if m := alphaAdd.FindStringSubmatch(step.Run); m != nil {
		staged = m[1]
	}
	if copied == "" || staged == "" {
		t.Fatalf("the alpha tap step does not both copy (%q) and stage (%q) a Formula file", copied, staged)
	}
	return step, copied, staged
}

func TestStableAndAlphaFormulasAreDifferentFiles(t *testing.T) {
	t.Parallel()

	cfg := readGoreleaser(t)
	_, copied, staged := alphaTapStep(t)
	if copied != staged {
		t.Errorf("the alpha step copies %s but stages %s", copied, staged)
	}
	if len(cfg.Brews) == 0 {
		t.Fatal(".goreleaser.yml has no brews entry")
	}
	rcEntries := 0
	for i, b := range cfg.Brews {
		file, err := formulaFile(cfg, b.Name)
		if err != nil {
			t.Errorf("brews[%d]: %v", i, err)
			continue
		}
		if b.Directory != "" && b.Directory != "Formula" {
			t.Errorf("brews[%d] writes into directory %q, the tap keeps formulas in Formula", i, b.Directory)
		}
		if file == copied {
			t.Errorf("brews[%d] writes Formula/%s, the file the alpha job writes (ci.yml)", i, file)
		}
		if b.Name != "marvel-rc" {
			continue
		}
		rcEntries++
		if skip := fmt.Sprint(b.SkipUpload); !strings.Contains(skip, "Prerelease") || strings.Contains(skip, "auto") {
			t.Errorf("the marvel-rc entry's skip_upload = %q; it must upload for a prerelease tag, which \"auto\" skips", skip)
		}
	}
	if rcEntries != 1 {
		t.Errorf("%d brews entries are named marvel-rc, want exactly 1", rcEntries)
	}
}

// The stable name is waiting on a decision, so it lives in one named place and
// the file says so, naming the ticket that holds the decision.
func TestStableFormulaNameIsOneNamedConstantWithATodo(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := readGoreleaser(t)
	var constant string
	for _, e := range cfg.Env {
		if k, _, ok := strings.Cut(e, "="); ok && k == "STABLE_FORMULA_NAME" {
			constant = e
		}
	}
	if constant == "" {
		t.Fatal("env has no STABLE_FORMULA_NAME entry")
	}
	if !strings.Contains(string(raw), "TODO(aae-orc-pd1oc)") {
		t.Error("the stable name has no TODO that cites the ticket aae-orc-pd1oc")
	}
	stable := 0
	for _, b := range cfg.Brews {
		if b.Name == "{{ .Env.STABLE_FORMULA_NAME }}" {
			stable++
			if s := fmt.Sprint(b.SkipUpload); s != "auto" {
				t.Errorf("the stable entry's skip_upload = %q, want auto so a prerelease never writes it", s)
			}
		}
	}
	if stable != 1 {
		t.Errorf("%d brews entries take their name from STABLE_FORMULA_NAME, want exactly 1", stable)
	}
}

var exactVersion = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

func TestGoreleaserVersionIsPinned(t *testing.T) {
	t.Parallel()

	jobs, err := ciJobs(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for name, job := range jobs {
		for _, s := range job.Steps {
			if !strings.Contains(s.Uses, "goreleaser-action@") {
				continue
			}
			found++
			v, _ := s.With["version"].(string)
			if !exactVersion.MatchString(v) {
				t.Errorf("job %s runs goreleaser-action with version %q, want one exact release such as v2.18.3", name, v)
			}
		}
	}
	if found != 1 {
		t.Errorf("release.yml has %d goreleaser-action steps, want 1", found)
	}
}

// The goreleaser job in ci.yml validates the stable config on every pull
// request without any privilege: read-only permissions, no secret, no token,
// the same pinned release as release.yml, and nothing that publishes.
func TestGoreleaserCheckJobIsPinnedAndUnprivileged(t *testing.T) {
	t.Parallel()

	ciPath := filepath.Join("..", "..", ".github", "workflows", "ci.yml")
	data, err := os.ReadFile(ciPath)
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]map[string]any `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	raw, ok := wf.Jobs["goreleaser-check"]
	if !ok {
		t.Fatal("ci.yml has no goreleaser-check job")
	}
	perms, _ := raw["permissions"].(map[string]any)
	if len(perms) != 1 || perms["contents"] != "read" {
		t.Errorf("goreleaser-check permissions = %v, want only contents: read", raw["permissions"])
	}
	body, err := yaml.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"secrets.", "TOKEN", "github.token"} {
		if strings.Contains(string(body), banned) {
			t.Errorf("goreleaser-check mentions %q; the job carries no secret and passes no token", banned)
		}
	}

	pinned := ""
	release, err := ciJobs(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range release {
		for _, s := range job.Steps {
			if strings.Contains(s.Uses, "goreleaser-action@") {
				pinned, _ = s.With["version"].(string)
			}
		}
	}
	if pinned == "" {
		t.Fatal("release.yml pins no goreleaser version")
	}

	jobs, err := ciJobs(ciPath)
	if err != nil {
		t.Fatal(err)
	}
	pinnedAction := regexp.MustCompile(`^step-security/goreleaser-action@[0-9a-f]{40}$`)
	checks, snapshots := 0, 0
	for _, s := range jobs["goreleaser-check"].Steps {
		if !strings.Contains(s.Uses, "goreleaser-action@") {
			continue
		}
		if !pinnedAction.MatchString(s.Uses) {
			t.Errorf("goreleaser step uses %q, want step-security/goreleaser-action at a full commit sha", s.Uses)
		}
		if v, _ := s.With["version"].(string); v != pinned {
			t.Errorf("goreleaser step runs version %q, release.yml runs %q", v, pinned)
		}
		if len(s.Env) != 0 {
			t.Errorf("goreleaser step sets env %v; it passes nothing", s.Env)
		}
		switch args, _ := s.With["args"].(string); {
		case args == "check":
			checks++
		case strings.HasPrefix(args, "release --snapshot"):
			snapshots++
		default:
			t.Errorf("goreleaser step args = %q, want check or release --snapshot", args)
		}
	}
	if checks != 1 || snapshots != 1 {
		t.Errorf("goreleaser-check runs %d check steps and %d snapshot steps, want 1 and 1", checks, snapshots)
	}
	// A formula pipe that fails does not fail goreleaser, so the job reads the
	// files itself, and refuses the file the alpha job owns.
	listed := false
	for _, s := range jobs["goreleaser-check"].Steps {
		if !strings.Contains(s.Run, "dist/homebrew") {
			continue
		}
		listed = true
		if !strings.Contains(s.Run, "-name 'marvel.rb'") || !strings.Contains(s.Run, "exit 1") {
			t.Errorf("the formula step does not fail on dist/homebrew/**/marvel.rb:\n%s", s.Run)
		}
		// The guard above runs inside an if, where set -e is off, so this
		// plain listing is what fails the job when dist/homebrew is missing.
		if !strings.Contains(s.Run, "find dist/homebrew -name '*.rb' | sort") {
			t.Error("the formula step does not list the formulas outside the if, so a missing dist/homebrew would pass")
		}
		if !strings.Contains(s.Run, "set -euo pipefail") {
			t.Error("the formula step does not run with set -euo pipefail, so a missing dist/homebrew would pass")
		}
	}
	if !listed {
		t.Error("goreleaser-check has no step that reads dist/homebrew")
	}
}
