package toolchain

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A test that needs nats-server or tmux fails, rather than skips, only for a
// tool named in MARVEL_REQUIRED_TOOLS (internal/requiretool, marvel#646). The
// list belongs to the job that installs the tools, and names only tools that
// job installs, so a job never promises what it did not provide.
func TestJobsListTheToolsTheyInstall(t *testing.T) {
	t.Parallel()
	ci, err := ciJobs(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"quality-gate":      {"nats-server"},
		"restructure-macos": {"nats-server", "tmux"},
	}
	for job, tools := range want {
		j, ok := ci[job]
		if !ok {
			t.Errorf("ci.yml has no job %q", job)
			continue
		}
		got := j.Env["MARVEL_REQUIRED_TOOLS"]
		if got != strings.Join(tools, ",") {
			t.Errorf("job %s: MARVEL_REQUIRED_TOOLS = %q, want %q", job, got, strings.Join(tools, ","))
		}
	}
	for job, j := range ci {
		list := j.Env["MARVEL_REQUIRED_TOOLS"]
		if list == "" {
			continue
		}
		for _, tool := range strings.Split(list, ",") {
			installed := false
			for _, s := range j.Steps {
				if strings.HasPrefix(s.Name, "Install") && strings.Contains(s.Name, tool) {
					installed = true
				}
			}
			if !installed {
				t.Errorf("job %s lists %s as required, but no Install step of the job names it", job, tool)
			}
		}
	}
}

// The main test step keeps its log, and a step after it prints the skip counts.
// The count is information only: the step does not set a threshold.
func TestQualityGatePrintsSkipCounts(t *testing.T) {
	t.Parallel()
	jobs, err := ciJobs(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	j := jobs["quality-gate"]
	var tests, counts string
	for _, s := range j.Steps {
		switch s.Name {
		case "Run tests with race detector":
			tests = s.Run
		case "Print skip counts per package":
			counts = s.Run
		}
	}
	if !strings.Contains(tests, "tee") {
		t.Errorf("the test step does not keep its log:\n%s", tests)
	}
	if !strings.Contains(counts, "scripts/skip-counts.sh") {
		t.Errorf("no step runs scripts/skip-counts.sh:\n%s", counts)
	}
}

func runSkipCounts(t *testing.T, log string) (int, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "go-test.log")
	if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(filepath.Join("..", "..", "scripts", "skip-counts.sh"), path).CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, string(out)
	case errors.As(err, &exit):
		return exit.ExitCode(), string(out)
	}
	t.Fatalf("run skip-counts.sh: %v", err)
	return 0, ""
}

func TestSkipCountsPrintsOneLinePerPackageThatSkipped(t *testing.T) {
	t.Parallel()
	log := `=== RUN   TestA
--- PASS: TestA (0.00s)
=== RUN   TestB
    b_test.go:9: nats-server not on PATH
--- SKIP: TestB (0.00s)
=== RUN   TestC
--- SKIP: TestC (0.00s)
=== RUN   TestC/sub
    --- SKIP: TestC/sub (0.00s)
PASS
ok  	github.com/arcavenae/marvel/internal/bus	0.5s
=== RUN   TestD
--- PASS: TestD (0.00s)
ok  	github.com/arcavenae/marvel/internal/daemon	0.1s
=== RUN   TestE
--- SKIP: TestE (0.00s)
FAIL
FAIL	github.com/arcavenae/marvel/internal/team	0.2s
?   	github.com/arcavenae/marvel/cmd/simulator	[no test files]
`
	code, out := runSkipCounts(t, log)
	if code != 0 {
		t.Fatalf("exit %d, want 0 (information only):\n%s", code, out)
	}
	for _, want := range []string{
		"github.com/arcavenae/marvel/internal/bus: 3 skipped",
		"github.com/arcavenae/marvel/internal/team: 1 skipped",
		"total: 4 skipped",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "internal/daemon") {
		t.Errorf("a package with no skips is listed:\n%s", out)
	}
}

func TestSkipCountsExitsZeroWithNoSkips(t *testing.T) {
	t.Parallel()
	code, out := runSkipCounts(t, "--- PASS: TestA (0.00s)\nok  \tgithub.com/arcavenae/marvel/internal/bus\t0.5s\n")
	if code != 0 || !strings.Contains(out, "total: 0 skipped") {
		t.Errorf("exit %d, output:\n%s", code, out)
	}
}

// Information only: a missing log is reported and does not fail the job.
func TestSkipCountsExitsZeroWithoutALog(t *testing.T) {
	t.Parallel()
	out, err := exec.Command(filepath.Join("..", "..", "scripts", "skip-counts.sh"), filepath.Join(t.TempDir(), "absent.log")).CombinedOutput()
	if err != nil {
		t.Errorf("exit with a missing log: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "skip-counts") {
		t.Errorf("a missing log is not reported:\n%s", out)
	}
}
