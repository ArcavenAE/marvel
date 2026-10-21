package toolchain

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// restructurePackages are the packages docs/design/code-restructure-plan.md
// moves code out of or into: Move 1 takes the client from internal/daemon to
// internal/daemonrpc, and Move 2 takes the bus supervisor lifecycle from
// internal/bus to internal/workload, with the restart schedule in
// internal/backoff. CI tests them only on Linux unless a macOS job runs them,
// so a move that behaves differently on darwin would merge green
// (aae-orc-xe1pw). All five exist on main; a package the restructure adds
// later joins this list in the PR that creates it.
var restructurePackages = []string{
	"internal/daemon",
	"internal/daemonrpc",
	"internal/bus",
	"internal/workload",
	"internal/backoff",
}

// TestRestructurePackagesRunOnMacOS requires one macOS job to run every
// restructure package, and to install the two tools whose absence
// makes those tests skip rather than run: tmux (internal/daemon) and
// nats-server (internal/bus). The nats-server version is the job's
// NATS_SERVER_VERSION, which TestNatsServerPinMatchesCI holds to the mise pin
// in every job.
func TestRestructurePackagesRunOnMacOS(t *testing.T) {
	t.Parallel()

	repo := filepath.Join("..", "..")
	for _, pkg := range restructurePackages {
		if !hasTests(t, filepath.Join(repo, pkg)) {
			t.Fatalf("%s has no test files; the list is stale or the repo root is wrong", pkg)
		}
	}
	want := restructurePackages

	jobs, err := ciJobs(filepath.Join(repo, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var reasons []string
	for _, name := range slices.Sorted(maps.Keys(jobs)) {
		job := jobs[name]
		if !strings.HasPrefix(job.RunsOn, "macos") {
			continue
		}
		missing := macOSGaps(job, want)
		if len(missing) == 0 {
			return
		}
		reasons = append(reasons, fmt.Sprintf("job %s: %s", name, strings.Join(missing, "; ")))
	}
	if len(reasons) == 0 {
		reasons = []string{"no job runs on macOS"}
	}
	t.Errorf("no macOS job runs the restructure packages %v with tmux and nats-server installed:\n  %s",
		want, strings.Join(reasons, "\n  "))
}

func TestMacOSGaps(t *testing.T) {
	t.Parallel()
	installStep := ciJobStep{
		Env: map[string]string{"NATS_SERVER_VERSION": "v2.15.0"},
		Run: "brew install tmux\ngo install \"github.com/nats-io/nats-server/v2@${NATS_SERVER_VERSION}\"",
	}
	const testLine = "go test -v -count=1 -race ./internal/bus/... ./internal/daemon/..."
	job := func(steps ...ciJobStep) ciJob { return ciJob{Steps: steps} }
	run := func(r string) ciJobStep { return ciJobStep{Run: r} }
	withJob := func(j ciJob, f func(*ciJob)) ciJob { f(&j); return j }
	withStep := func(st ciJobStep, f func(*ciJobStep)) ciJobStep { f(&st); return st }
	good := job(installStep, run(testLine))
	pkgs := []string{"internal/bus", "internal/daemon"}
	for _, tc := range []struct {
		name string
		job  ciJob
		gaps int
	}{
		{"all run, both installed", good, 0},
		{"one package missing", job(installStep, run("go test -race ./internal/bus/...")), 1},
		{"a prefix is not the package", job(installStep, run("go test -race ./internal/busx/... ./internal/daemon/...")), 1},
		{"named outside go test", job(installStep, run("echo -race ./internal/bus/... ./internal/daemon/...")), 2},
		{"no -race", job(installStep, run("go test -v -count=1 ./internal/bus/... ./internal/daemon/...")), 2},
		{"the test line ignores a failure", job(installStep, run(testLine+" || true")), 2},
		{"no tmux", job(ciJobStep{
			Env: installStep.Env,
			Run: "go install \"github.com/nats-io/nats-server/v2@${NATS_SERVER_VERSION}\"",
		}, run(testLine)), 1},
		{"the tmux install ignores a failure", job(withStep(installStep, func(st *ciJobStep) {
			st.Run = strings.Replace(st.Run, "brew install tmux", "brew install tmux || true", 1)
		}), run(testLine)), 1},
		{"nats-server at a literal version", job(ciJobStep{
			Run: "brew install tmux\ngo install github.com/nats-io/nats-server/v2@v2.14.0",
		}, run(testLine)), 1},
		{"the job has an if", withJob(good, func(j *ciJob) { j.If = "false" }), 1},
		{"the job continues on error", withJob(good, func(j *ciJob) { j.ContinueOnError = true }), 1},
		{"the job continues on an expression", withJob(good, func(j *ciJob) { j.ContinueOnError = "${{ matrix.experimental }}" }), 1},
		{"continue-on-error false on the job", withJob(good, func(j *ciJob) { j.ContinueOnError = false }), 0},
		{"continue-on-error \"false\" on the job", withJob(good, func(j *ciJob) { j.ContinueOnError = "false" }), 0},
		{"the test step has an if", job(installStep, withStep(run(testLine), func(st *ciJobStep) { st.If = "github.event_name == 'push'" })), 3},
		{"the test step continues on error", job(installStep, withStep(run(testLine), func(st *ciJobStep) { st.ContinueOnError = true })), 3},
		{"the install step continues on error", job(withStep(installStep, func(st *ciJobStep) { st.ContinueOnError = true }), run(testLine)), 3},
		{"nothing", ciJob{}, 4},
	} {
		if got := macOSGaps(tc.job, pkgs); len(got) != tc.gaps {
			t.Errorf("%s: gaps = %q, want %d", tc.name, got, tc.gaps)
		}
	}
}

type ciJobStep struct {
	Name            string            `yaml:"name"`
	If              string            `yaml:"if"`
	ContinueOnError any               `yaml:"continue-on-error"`
	Env             map[string]string `yaml:"env"`
	Run             string            `yaml:"run"`
}

type ciJob struct {
	RunsOn          string      `yaml:"runs-on"`
	If              string      `yaml:"if"`
	ContinueOnError any         `yaml:"continue-on-error"`
	Steps           []ciJobStep `yaml:"steps"`
}

func ciJobs(path string) (map[string]ciJob, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var wf struct {
		Jobs map[string]ciJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return wf.Jobs, nil
}

// macOSGaps lists what the job lacks: each package no go test line names, and
// each tool it does not install. A package counts when a go test line names it
// as ./<pkg> or ./<pkg>/... exactly and carries -race; nats-server counts only
// when installed at the step's NATS_SERVER_VERSION, so the version stays under
// the pin check.
//
// Only what can fail the job counts. A job that sets if, or a continue-on-error
// that is not false, is a gap, since it may never run or never go red. A step
// that sets either is reported and read as absent, and a line with || on it
// counts for nothing, since it can swallow the failure.
func macOSGaps(job ciJob, pkgs []string) []string {
	var gaps []string
	for _, why := range mayNotFail(job.If, job.ContinueOnError) {
		gaps = append(gaps, "the job sets "+why)
	}
	var lines []string
	for i, step := range job.Steps {
		if whys := mayNotFail(step.If, step.ContinueOnError); len(whys) > 0 {
			gaps = append(gaps, fmt.Sprintf("step %d (%s) sets %s, so it is not counted", i, step.Name, strings.Join(whys, " and ")))
			continue
		}
		for _, line := range strings.Split(step.Run, "\n") {
			if strings.Contains(line, "||") {
				continue
			}
			lines = append(lines, line)
		}
	}
	natsLines := natsInstallLines(job)
	for _, pkg := range pkgs {
		if !linesTestPackage(lines, pkg) {
			gaps = append(gaps, "does not test ./"+pkg+" with -race")
		}
	}
	tmux := false
	for _, line := range lines {
		if strings.Contains(line, "brew install") && slices.Contains(strings.Fields(line), "tmux") {
			tmux = true
		}
	}
	nats := false
	for _, line := range natsLines {
		if slices.Contains(lines, line) {
			nats = true
		}
	}
	if !tmux {
		gaps = append(gaps, "does not brew install tmux")
	}
	if !nats {
		gaps = append(gaps, "does not go install nats-server at NATS_SERVER_VERSION")
	}
	return gaps
}

// mayNotFail names the keys that let a job or step skip or stay green: an if
// of any kind, and a continue-on-error that is not false.
func mayNotFail(cond string, continueOnError any) []string {
	var whys []string
	if strings.TrimSpace(cond) != "" {
		whys = append(whys, "if: "+cond)
	}
	switch v := continueOnError.(type) {
	case nil:
	case bool:
		if v {
			whys = append(whys, "continue-on-error: true")
		}
	case string:
		if strings.TrimSpace(v) != "false" {
			whys = append(whys, "continue-on-error: "+v)
		}
	default:
		whys = append(whys, fmt.Sprintf("continue-on-error: %v", v))
	}
	return whys
}

// natsInstallLines are the lines that go install nats-server at the step's
// NATS_SERVER_VERSION.
func natsInstallLines(job ciJob) []string {
	var out []string
	for _, step := range job.Steps {
		if _, ok := step.Env["NATS_SERVER_VERSION"]; !ok {
			continue
		}
		for _, line := range strings.Split(step.Run, "\n") {
			if strings.Contains(line, "go install") &&
				strings.Contains(line, "github.com/nats-io/nats-server/v2@${NATS_SERVER_VERSION}") {
				out = append(out, line)
			}
		}
	}
	return out
}

func linesTestPackage(lines []string, pkg string) bool {
	for _, line := range lines {
		fields := strings.Fields(line)
		if !strings.Contains(line, "go test") || !slices.Contains(fields, "-race") {
			continue
		}
		if slices.Contains(fields, "./"+pkg) || slices.Contains(fields, "./"+pkg+"/...") {
			return true
		}
	}
	return false
}

// hasTests reports whether dir holds a Go test file.
func hasTests(t *testing.T, dir string) bool {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}
	return len(matches) > 0
}
