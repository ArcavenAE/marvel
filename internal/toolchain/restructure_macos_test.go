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
	install := ciJob{Steps: []ciJobStep{{
		Env: map[string]string{"NATS_SERVER_VERSION": "v2.15.0"},
		Run: "brew install tmux\ngo install \"github.com/nats-io/nats-server/v2@${NATS_SERVER_VERSION}\"",
	}}}
	withTest := func(j ciJob, run string) ciJob {
		j.Steps = append(slices.Clone(j.Steps), ciJobStep{Run: run})
		return j
	}
	pkgs := []string{"internal/bus", "internal/daemon"}
	for _, tc := range []struct {
		name string
		job  ciJob
		gaps int
	}{
		{"all run, both installed", withTest(install, "go test ./internal/bus/... ./internal/daemon -race"), 0},
		{"one package missing", withTest(install, "go test ./internal/bus/... -race"), 1},
		{"a prefix is not the package", withTest(install, "go test ./internal/busx/... ./internal/daemon/..."), 1},
		{"named outside go test", withTest(install, "echo ./internal/bus/... ./internal/daemon/..."), 2},
		{"no tmux", withTest(ciJob{Steps: []ciJobStep{{
			Env: install.Steps[0].Env,
			Run: "go install \"github.com/nats-io/nats-server/v2@${NATS_SERVER_VERSION}\"",
		}}}, "go test ./internal/bus/... ./internal/daemon/..."), 1},
		{"nats-server at a literal version", withTest(ciJob{Steps: []ciJobStep{{
			Run: "brew install tmux\ngo install github.com/nats-io/nats-server/v2@v2.14.0",
		}}}, "go test ./internal/bus/... ./internal/daemon/..."), 1},
		{"nothing", ciJob{}, 4},
	} {
		if got := macOSGaps(tc.job, pkgs); len(got) != tc.gaps {
			t.Errorf("%s: gaps = %q, want %d", tc.name, got, tc.gaps)
		}
	}
}

type ciJobStep struct {
	Name string            `yaml:"name"`
	Env  map[string]string `yaml:"env"`
	Run  string            `yaml:"run"`
}

type ciJob struct {
	RunsOn string      `yaml:"runs-on"`
	Steps  []ciJobStep `yaml:"steps"`
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
// as ./<pkg> or ./<pkg>/... exactly; nats-server counts only when installed at
// the step's NATS_SERVER_VERSION, so the version stays under the pin check.
func macOSGaps(job ciJob, pkgs []string) []string {
	var gaps []string
	for _, pkg := range pkgs {
		if !jobTestsPackage(job, pkg) {
			gaps = append(gaps, "does not test ./"+pkg)
		}
	}
	tmux, nats := false, false
	for _, step := range job.Steps {
		for _, line := range strings.Split(step.Run, "\n") {
			if strings.Contains(line, "brew install") && slices.Contains(strings.Fields(line), "tmux") {
				tmux = true
			}
			if _, ok := step.Env["NATS_SERVER_VERSION"]; ok &&
				strings.Contains(line, "go install") &&
				strings.Contains(line, "github.com/nats-io/nats-server/v2@${NATS_SERVER_VERSION}") {
				nats = true
			}
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

func jobTestsPackage(job ciJob, pkg string) bool {
	for _, step := range job.Steps {
		for _, line := range strings.Split(step.Run, "\n") {
			if !strings.Contains(line, "go test") {
				continue
			}
			for _, f := range strings.Fields(line) {
				if f == "./"+pkg || f == "./"+pkg+"/..." {
					return true
				}
			}
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
