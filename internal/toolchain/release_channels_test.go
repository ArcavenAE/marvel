package toolchain

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"text/template"

	"gopkg.in/yaml.v3"
)

// The alpha channel and the stable channel are built by two different
// pipelines, and both end with a formula in the same tap. A formula
// file written by both is overwritten by whichever runs last, so a host that
// installed one channel is moved to the other by `brew upgrade`. These tests
// hold the files apart, and hold the stable build tool to one exact version.

// goreleaserConfig is the part of .goreleaser.yml the tests read.
type goreleaserConfig struct {
	ProjectName string   `yaml:"project_name"`
	Env         []string `yaml:"env"`
	Brews       []struct {
		Name       string `yaml:"name"`
		Directory  string `yaml:"directory"`
		SkipUpload any    `yaml:"skip_upload"`
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

// renderSkipUpload renders a skip_upload value the way goreleaser does for the
// one field these entries use, .Prerelease, without running goreleaser.
func renderSkipUpload(skip, prerelease string) (string, error) {
	tmpl, err := template.New("skip_upload").Option("missingkey=error").Parse(skip)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	if err := tmpl.Execute(&out, struct{ Prerelease string }{prerelease}); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
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

// alphaFormula is the tap file the alpha channel's formula lands in. The tap's
// own update-formulas workflow writes it; this repo's ci.yml does not.
const alphaFormula = "marvel.rb"

// noAlphaTapStep fails when ci.yml names the tap repository or its token. The
// alpha formula reaches the tap only through the tap's own workflow, so its
// commit is the signed record; a second writer here would add an unsigned one.
// The check reads the raw file, so a step that clones into any directory name,
// or passes the token through env or with, is caught as well as one that
// writes into tap-repo/Formula.
func noAlphaTapStep(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"homebrew-tap", "HOMEBREW_TAP_TOKEN"} {
		if strings.Contains(string(raw), name) {
			t.Errorf("ci.yml names %s; the tap's own workflow is the only alpha writer", name)
		}
	}
}

func TestStableAndAlphaFormulasAreDifferentFiles(t *testing.T) {
	t.Parallel()

	cfg := readGoreleaser(t)
	noAlphaTapStep(t)
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
		if file == alphaFormula {
			t.Errorf("brews[%d] writes Formula/%s, the file the alpha channel writes", i, file)
		}
		if b.Name != "marvel-rc" {
			continue
		}
		rcEntries++
		// Render the template for an rc tag and for a tag with no prerelease:
		// goreleaser skips the upload when the result is "true".
		for _, tc := range []struct{ prerelease, want string }{
			{"rc.1", "false"},
			{"", "true"},
		} {
			got, err := renderSkipUpload(fmt.Sprint(b.SkipUpload), tc.prerelease)
			if err != nil {
				t.Errorf("the marvel-rc entry's skip_upload %q does not render: %v", b.SkipUpload, err)
				break
			}
			if got != tc.want {
				t.Errorf("the marvel-rc entry's skip_upload renders %q for prerelease %q, want %q (it must upload for an rc tag and skip otherwise)",
					got, tc.prerelease, tc.want)
			}
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
		// The snapshot and the formula listing are the gate, and so is every
		// other step: only the check step may continue on error.
		isCheck := strings.Contains(s.Uses, "goreleaser-action@") && s.With["args"] == "check"
		if s.ContinueOnError != nil && !isCheck {
			t.Errorf("step %q sets continue-on-error = %v; only the check step may", s.Name, s.ContinueOnError)
		}
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
			// check exits 2 for a deprecated property, so it reports only.
			if s.ContinueOnError != true {
				t.Errorf("the check step has continue-on-error = %v, want true: it reports deprecations and does not gate", s.ContinueOnError)
			}
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
