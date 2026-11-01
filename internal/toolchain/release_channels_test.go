package toolchain

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
// own update-formulas workflow writes it (homebrew-tap's update-formulas.yml is
// the one tap writer for this file); no workflow in this repo does.
const alphaFormula = "marvel.rb"

// tapNames are the strings that mean a workflow reaches the tap repository or
// its token. GitHub resolves repository and secret names regardless of case, so
// they are matched in lower case: arcavenae/Homebrew-Tap and
// secrets.homebrew_tap_token reach the tap as well as the spellings below.
var tapNames = []string{"homebrew-tap", "homebrew_tap_token"}

// tapNamingWorkflows are the only workflow files allowed to name the tap, each
// with the reason. Neither writes the alpha formula: release.yml hands the token
// to goreleaser, whose rc and stable brews entries are held to their own file
// names below, and release-verify.yml only reads the tap's CI status.
var tapNamingWorkflows = map[string]string{
	"release.yml":        "passes the tap token to goreleaser, whose rc and stable brews entries write the rc and stable formulas",
	"release-verify.yml": "reads the tap's CI status with the token and writes nothing",
}

// tapNamingViolations returns, for each workflow file that names the tap or its
// token and is not in the allowlist, a message. Names are matched in lower case.
// workflows maps a file's base name to its text.
func tapNamingViolations(workflows map[string]string) []string {
	var out []string
	for name, text := range workflows {
		if _, ok := tapNamingWorkflows[name]; ok {
			continue
		}
		lower := strings.ToLower(text)
		for _, n := range tapNames {
			if strings.Contains(lower, n) {
				out = append(out, fmt.Sprintf("%s names %s; homebrew-tap's update-formulas.yml is the only alpha writer", name, n))
			}
		}
	}
	sort.Strings(out)
	return out
}

// noAlphaTapStep fails when any workflow outside the allowlist names the tap
// repository or its token. The alpha formula reaches the tap only through the
// tap's own update-formulas.yml, so its commit is the signed record; a second
// writer here would add an unsigned one. The check reads every workflow file raw,
// so a step in a new workflow, one that clones into any directory name, or one
// that passes the token through env or with, is caught as well as one that
// writes into tap-repo/Formula. An allowlisted file that no longer names the tap
// is stale and fails too, so the list cannot outlive its reason.
func noAlphaTapStep(t *testing.T) {
	t.Helper()
	dir := filepath.Join("..", "..", ".github", "workflows")
	var files []string
	for _, pat := range []string{"*.yml", "*.yaml"} {
		m, err := filepath.Glob(filepath.Join(dir, pat))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	workflows := map[string]string{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		workflows[filepath.Base(f)] = string(raw)
	}
	if _, ok := workflows["ci.yml"]; !ok {
		t.Fatalf("found %d workflow files and ci.yml is not among them; the guard would scan nothing", len(workflows))
	}
	for _, v := range tapNamingViolations(workflows) {
		t.Error(v)
	}
	for _, v := range staleTapAllowlist(workflows) {
		t.Error(v)
	}
}

// staleTapAllowlist returns a message for each allowlisted workflow that is
// missing from workflows or no longer names the tap.
func staleTapAllowlist(workflows map[string]string) []string {
	var out []string
	for name := range tapNamingWorkflows {
		text, ok := workflows[name]
		if !ok {
			out = append(out, fmt.Sprintf("allowlisted workflow %s does not exist; remove it from tapNamingWorkflows", name))
			continue
		}
		named := false
		for _, n := range tapNames {
			named = named || strings.Contains(strings.ToLower(text), n)
		}
		if !named {
			out = append(out, fmt.Sprintf("allowlisted workflow %s no longer names the tap; remove it from tapNamingWorkflows", name))
		}
	}
	sort.Strings(out)
	return out
}

func TestStaleTapAllowlist(t *testing.T) {
	t.Parallel()
	live := map[string]string{"release.yml": "HOMEBREW_TAP_TOKEN", "release-verify.yml": "arcavenae/Homebrew-Tap"}
	if got := staleTapAllowlist(live); len(got) != 0 {
		t.Errorf("live allowlist reported stale: %q", got)
	}
	if got := staleTapAllowlist(map[string]string{"release.yml": "HOMEBREW_TAP_TOKEN"}); len(got) != 1 {
		t.Errorf("a missing file: %q, want one message", got)
	}
	if got := staleTapAllowlist(map[string]string{"release.yml": "HOMEBREW_TAP_TOKEN", "release-verify.yml": "name: verify"}); len(got) != 1 {
		t.Errorf("a file that no longer names the tap: %q, want one message", got)
	}
}

func TestTapNamingViolations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		workflows map[string]string
		want      int
	}{
		{"nothing names the tap", map[string]string{"ci.yml": "name: ci\n"}, 0},
		{"the repository name", map[string]string{"ci.yml": "repository: arcavenae/homebrew-tap\n"}, 1},
		{"the repository name in another case", map[string]string{"ci.yml": "repository: arcavenae/Homebrew-Tap\n"}, 1},
		{"the secret name", map[string]string{"ci.yml": "token: ${{ secrets.HOMEBREW_TAP_TOKEN }}\n"}, 1},
		{"the secret name in lower case", map[string]string{"ci.yml": "token: ${{ secrets.homebrew_tap_token }}\n"}, 1},
		{"a workflow other than ci.yml", map[string]string{"ci.yml": "name: ci\n", "extra.yml": "repository: arcavenae/homebrew-tap\n"}, 1},
		{"an allowlisted workflow may name it", map[string]string{"release.yml": "token: ${{ secrets.HOMEBREW_TAP_TOKEN }}\n", "release-verify.yml": "--repo arcavenae/homebrew-tap\n"}, 0},
		{"both names in one file are two findings", map[string]string{"ci.yml": "homebrew-tap HOMEBREW_TAP_TOKEN\n"}, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tapNamingViolations(tc.workflows); len(got) != tc.want {
				t.Errorf("violations = %q, want %d", got, tc.want)
			}
		})
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
