package toolchain

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// probeFile is the one-off check that the jobs which run only on a push to
// main (Build Binaries, Create Release, Attest) work on the runner image the
// ubuntu-latest label moves to on 2026-10-19 (#593). Those jobs are pinned to
// ubuntu-24.04, so nothing else runs them on the new image until someone
// unpins. The probe must be safe to run at any time: it builds and checks,
// and publishes nothing.
const probeFile = "probe-ubuntu-26.yml"

type probeWorkflow struct {
	On          map[string]any    `yaml:"on"`
	Permissions map[string]string `yaml:"permissions"`
	Jobs        map[string]struct {
		RunsOn      string            `yaml:"runs-on"`
		Environment any               `yaml:"environment"`
		Permissions map[string]string `yaml:"permissions"`
		Steps       []struct {
			Uses string            `yaml:"uses"`
			Run  string            `yaml:"run"`
			With map[string]string `yaml:"with"`
			Env  map[string]string `yaml:"env"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func loadProbe(t *testing.T) (probeWorkflow, string) {
	t.Helper()
	path := filepath.Join("..", "..", ".github", "workflows", probeFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the probe workflow: %v", err)
	}
	var wf probeWorkflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return wf, string(data)
}

// The probe runs on the Ubuntu 26.04 label, on the one job it has.
func TestProbeRunsOnUbuntu2604(t *testing.T) {
	t.Parallel()
	wf, _ := loadProbe(t)
	if len(wf.Jobs) != 1 {
		t.Fatalf("probe has %d jobs, want exactly 1", len(wf.Jobs))
	}
	for name, job := range wf.Jobs {
		if job.RunsOn != "ubuntu-26.04" {
			t.Errorf("job %s runs on %q, want ubuntu-26.04", name, job.RunsOn)
		}
	}
}

// The probe starts only by hand, or when its own file changes in a pull
// request. A push, a schedule or a release would run it unattended.
func TestProbeStartsOnlyByHandOrOnItsOwnChange(t *testing.T) {
	t.Parallel()
	wf, _ := loadProbe(t)
	for trigger := range wf.On {
		if trigger != "workflow_dispatch" && trigger != "pull_request" {
			t.Errorf("trigger %q, want only workflow_dispatch and pull_request", trigger)
		}
	}
	if _, ok := wf.On["workflow_dispatch"]; !ok {
		t.Error("the probe has no workflow_dispatch trigger")
	}
	pr, ok := wf.On["pull_request"].(map[string]any)
	if !ok {
		t.Fatal("pull_request carries no path filter, so every pull request would run the probe")
	}
	paths, _ := pr["paths"].([]any)
	if len(paths) != 1 || paths[0] != ".github/workflows/"+probeFile {
		t.Errorf("pull_request paths = %v, want only the probe's own file", paths)
	}
}

// The probe is read-only: contents read at the top, nothing wider anywhere,
// no secrets, no environment, and none of the commands that publish.
func TestProbePublishesNothing(t *testing.T) {
	t.Parallel()
	wf, raw := loadProbe(t)
	text := shComment.ReplaceAllString(raw, "")
	if len(wf.Permissions) != 1 || wf.Permissions["contents"] != "read" {
		t.Errorf("top-level permissions = %v, want only contents: read", wf.Permissions)
	}
	for name, job := range wf.Jobs {
		if len(job.Permissions) != 0 {
			t.Errorf("job %s sets its own permissions %v, want none", name, job.Permissions)
		}
		if job.Environment != nil {
			t.Errorf("job %s names an environment %v, want none", name, job.Environment)
		}
	}
	for _, banned := range []string{
		"secrets.", "GITHUB_TOKEN", "id-token", "write",
		"gh release", "git push", "git clone", "attest", "upload-artifact",
	} {
		if strings.Contains(text, banned) {
			t.Errorf("the probe mentions %q, which belongs to a publishing step", banned)
		}
	}
}

var shaPin = regexp.MustCompile(`@[0-9a-f]{40}$`)

// Every action is pinned to a commit, and the runner is hardened first, as
// in the repo's other workflows.
func TestProbePinsActionsAndHardensTheRunner(t *testing.T) {
	t.Parallel()
	wf, _ := loadProbe(t)
	for name, job := range wf.Jobs {
		if len(job.Steps) == 0 {
			t.Fatalf("job %s has no steps", name)
		}
		first := job.Steps[0]
		if !strings.HasPrefix(first.Uses, "step-security/harden-runner@") || first.With["egress-policy"] != "audit" {
			t.Errorf("job %s first step = %q with %v, want harden-runner in audit mode", name, first.Uses, first.With)
		}
		for _, step := range job.Steps {
			if step.Uses != "" && !shaPin.MatchString(step.Uses) {
				t.Errorf("job %s uses %q, want a 40-character commit pin", name, step.Uses)
			}
		}
	}
}

// The probe covers what the main-only jobs do on the runner: the four
// cross-builds, and the tools the release job leans on.
func TestProbeExercisesTheMainOnlyJobs(t *testing.T) {
	t.Parallel()
	_, text := loadProbe(t)
	for _, want := range []string{
		"GOOS=darwin GOARCH=arm64 go build",
		"GOOS=darwin GOARCH=amd64 go build",
		"GOOS=linux  GOARCH=amd64 go build",
		"GOOS=linux  GOARCH=arm64 go build",
		"sha256sum",
		"sed -i",
		"diff --cached --quiet",
		"gh --version",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the probe never runs %q", want)
		}
	}
}

// The probe uses only the actions it needs, and the checkout leaves no
// credential in the repository config.
func TestProbeUsesOnlyTheActionsItNeeds(t *testing.T) {
	t.Parallel()
	wf, _ := loadProbe(t)
	allowed := map[string]bool{
		"step-security/harden-runner": true,
		"actions/checkout":            true,
		"actions/setup-go":            true,
	}
	for name, job := range wf.Jobs {
		for _, step := range job.Steps {
			if step.Uses == "" {
				continue
			}
			action, _, _ := strings.Cut(step.Uses, "@")
			if !allowed[action] {
				t.Errorf("job %s uses %q, want only %v", name, action, allowed)
			}
			if action == "actions/checkout" && step.With["persist-credentials"] != "false" {
				t.Errorf("job %s checkout keeps credentials: persist-credentials = %q", name, step.With["persist-credentials"])
			}
		}
	}
}

var (
	ghCall = regexp.MustCompile(`\bgh\b[ \t]+(\S+)`)
	// gitCall captures the subcommand, skipping the global options -C <dir>
	// and -c <key=value>.
	gitCall = regexp.MustCompile(`\bgit\b(?:[ \t]+-[Cc][ \t]+\S+)*[ \t]+(\S+)`)
	// shComment matches whole-line comments only. A # inside a quoted
	// string follows other text on its line, and treating it as a comment
	// would hide whatever comes after it.
	shComment  = regexp.MustCompile(`(?m)^[ \t]*#.*$`)
	localGitOK = map[string]bool{
		"--version": true, "init": true, "config": true, "add": true, "diff": true,
		"commit": true, "status": true, "log": true, "rev-parse": true, "show": true,
	}
)

// shellViolations lists the gh and git calls in a run block that are not
// local: gh other than --version, and git subcommands outside localGitOK.
// Whole-line comments are skipped.
func shellViolations(run string) []string {
	run = shComment.ReplaceAllString(run, "")
	var out []string
	for _, m := range ghCall.FindAllStringSubmatch(run, -1) {
		if m[1] != "--version" {
			out = append(out, "runs gh "+m[1]+", want only gh --version")
		}
	}
	for _, m := range gitCall.FindAllStringSubmatch(run, -1) {
		if !localGitOK[m[1]] {
			out = append(out, "runs git "+m[1]+", want only local subcommands")
		}
	}
	return out
}

// shellViolations must catch a publishing call that follows a quoted #, which
// is not a comment, and must let through a note that sits on a line of its
// own and local git reads with global options.
func TestShellViolationsReadsComments(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		run  string
		bad  bool
	}{
		{"quoted hash then gh release", `echo "build #1" && gh release create x`, true},
		{"single-quoted hash then gh release", `echo 'step #2'; gh release create x`, true},
		{"quoted hash then git push", `echo "see #1" && git -C tap-repo push origin main`, true},
		{"whole-line note naming gh", "# gh release create is what the real job does\nls", false},
		{"indented note naming git push", "  # git push is not run here\nls", false},
		{"git log with -C and -c", "git -C tap-repo -c user.name=probe log --oneline", false},
		{"alias name is an unknown subcommand", "git config alias.p '!git push'\ngit p", true},
		{"alias set with -c then run", "git -c alias.p='!git push' p", true},
		{"gh version", "gh --version", false},
		{"an echoed sentence containing the word alias", `echo "no alias is set"`, false},
	} {
		if got := len(shellViolations(tc.run)) > 0; got != tc.bad {
			t.Errorf("%s: flagged = %v, want %v, for %q", tc.name, got, tc.bad, tc.run)
		}
	}
}

// The shell steps reach no remote and carry no credential: every gh the file
// spells is gh --version, git runs only local subcommands, and no step sets a
// token, logs in, or calls a network tool. Banning the word GITHUB_TOKEN is not
// enough, since a step can name the token another way or publish with a
// different command. Whole-line comments are ignored, so a note that names gh
// is not a call. A git alias renames a remote subcommand, and the alias name
// shows up as an unknown subcommand.
func TestProbeShellStepsStayLocal(t *testing.T) {
	t.Parallel()
	wf, raw := loadProbe(t)
	text := shComment.ReplaceAllString(raw, "")
	for name, job := range wf.Jobs {
		for _, step := range job.Steps {
			for k, v := range step.Env {
				lower := strings.ToLower(k + "=" + v)
				for _, token := range []string{"token", "secret", "password", "credential", "github."} {
					if strings.Contains(lower, token) {
						t.Errorf("job %s step env %s=%q mentions %q", name, k, v, token)
					}
				}
			}
			for _, v := range shellViolations(step.Run) {
				t.Errorf("job %s: %s", name, v)
			}
		}
	}
	for _, banned := range []string{"docker", "curl", "wget", "login", "action-gh-release", "github.token", "github.event.pull_request.head"} {
		if strings.Contains(text, banned) {
			t.Errorf("the probe mentions %q, which reaches a remote or carries a credential", banned)
		}
	}
}
