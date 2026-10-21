package toolchain

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const busPkg = "github.com/arcavenae/marvel/internal/bus"

// runNoSkips runs scripts/no-skips.sh on a go test -json stream and returns its
// exit code and combined output.
func runNoSkips(t *testing.T, stream, pkg string) (int, string) {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatalf("jq is not on PATH; the gate needs it: %v", err)
	}
	path := filepath.Join(t.TempDir(), "tests.json")
	if err := os.WriteFile(path, []byte(stream), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(filepath.Join("..", "..", "scripts", "no-skips.sh"), path, pkg).CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, string(out)
	case errors.As(err, &exit):
		return exit.ExitCode(), string(out)
	}
	t.Fatalf("run no-skips.sh: %v", err)
	return 0, ""
}

func ev(action, pkg, test string) string {
	if test == "" {
		return `{"Action":"` + action + `","Package":"` + pkg + `"}` + "\n"
	}
	return `{"Action":"` + action + `","Package":"` + pkg + `","Test":"` + test + `"}` + "\n"
}

// The gate fails a Linux run where an internal/bus test skipped, or where no
// bus test ran at all (code-restructure-plan.md, PR M2-1; aae-orc-p0zkz).
func TestNoSkipsGate(t *testing.T) {
	t.Parallel()
	other := "github.com/arcavenae/marvel/internal/daemon"
	sibling := busPkg + "x"
	ran := ev("run", busPkg, "TestA") + ev("pass", busPkg, "TestA") + ev("pass", busPkg, "")
	for _, tc := range []struct {
		name, stream string
		want         int
		names        string
	}{
		{"every bus test passed", ran, 0, ""},
		{"a bus test skipped", ran + ev("skip", busPkg, "TestB"), 1, "TestB"},
		{"a bus subtest skipped", ran + ev("skip", busPkg, "TestA/sub"), 1, "TestA/sub"},
		{"each of two skips on its own indented line", ran + ev("skip", busPkg, "TestB") + ev("skip", busPkg, "TestC"), 1, "\n  " + busPkg + " TestC"},
		{"a test under internal/bus skipped", ran + ev("skip", busPkg+"/inner", "TestC"), 1, "TestC"},
		{"a skip in another package", ran + ev("skip", other, "TestD"), 0, ""},
		{"a skip in a package that only shares the prefix", ran + ev("skip", sibling, "TestE"), 0, ""},
		{"internal/bus has no test files", ev("skip", busPkg, ""), 1, busPkg},
		{"no test files at the top, a package under it passed", ev("skip", busPkg, "") + ev("pass", busPkg+"/inner", "TestH"), 1, "no test files"},
		{"no bus test ran", ev("pass", other, "TestF"), 1, busPkg},
		{"only a sibling package's tests passed", ev("pass", sibling, "TestG"), 1, busPkg},
		{"the stream is not go test -json", "ok  \t" + busPkg + "\t1.0s\n", 2, ""},
	} {
		got, out := runNoSkips(t, tc.stream, busPkg)
		switch {
		case got != tc.want:
			t.Errorf("%s: exit %d, want %d; output:\n%s", tc.name, got, tc.want, out)
		case tc.names != "" && !strings.Contains(out, tc.names):
			t.Errorf("%s: output does not name %s:\n%s", tc.name, tc.names, out)
		}
	}
}

// The Linux job runs internal/bus with -json and passes the stream to the gate
// in the same step, after nats-server is installed, so a skip there fails the
// job rather than reading as a pass.
func TestLinuxJobGatesBusSkips(t *testing.T) {
	t.Parallel()
	jobs, err := ciJobs(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	job, ok := jobs["quality-gate"]
	if !ok {
		t.Fatal("ci.yml has no quality-gate job")
	}
	if !strings.HasPrefix(job.RunsOn, "ubuntu") {
		t.Fatalf("quality-gate runs on %q, want ubuntu", job.RunsOn)
	}
	nats, gated := -1, -1
	for i, step := range job.Steps {
		if strings.Contains(step.Run, "nats-server") && nats < 0 {
			nats = i
		}
		if busSkipsGated(step.Run) {
			gated = i
		}
	}
	switch {
	case gated < 0:
		t.Error("no quality-gate step runs ./internal/bus/... with go test -json and passes the stream to scripts/no-skips.sh")
	case nats < 0 || gated < nats:
		t.Errorf("the bus skip gate (step %d) runs before nats-server is installed (step %d), so every broker test would skip", gated, nats)
	}
}

func TestBusSkipsGated(t *testing.T) {
	t.Parallel()
	test := `go test -json -count=1 ./internal/bus/... > "${RUNNER_TEMP}/bus.json"`
	gate := `scripts/no-skips.sh "${RUNNER_TEMP}/bus.json" ` + busPkg
	for _, tc := range []struct {
		name, run string
		want      bool
	}{
		{"test then gate", "set -euo pipefail\n" + test + "\n" + gate, true},
		{"gate before the test", gate + "\n" + test, false},
		{"no -json", strings.Replace(test, "-json ", "", 1) + "\n" + gate, false},
		{"another package", strings.Replace(test, "internal/bus", "internal/daemon", 1) + "\n" + gate, false},
		{"gate on another package", test + "\n" + strings.Replace(gate, "internal/bus", "internal/daemon", 1), false},
		{"no gate", test, false},
		{"gate commented out", test + "\n# " + gate, false},
	} {
		if got := busSkipsGated(tc.run); got != tc.want {
			t.Errorf("%s: busSkipsGated = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// busSkipsGated reports whether a run script has a go test -json line naming
// ./internal/bus/... followed by a line that runs scripts/no-skips.sh for the
// bus package.
func busSkipsGated(run string) bool {
	tested := false
	for _, line := range strings.Split(run, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if strings.Contains(line, "go test") && slices.Contains(fields, "-json") && slices.Contains(fields, "./internal/bus/...") {
			tested = true
			continue
		}
		if tested && len(fields) > 0 && strings.HasSuffix(fields[0], "scripts/no-skips.sh") && slices.Contains(fields, busPkg) {
			return true
		}
	}
	return false
}
