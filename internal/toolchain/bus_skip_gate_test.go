package toolchain

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const busPkg = "github.com/arcavenae/marvel/internal/bus"

// runNoSkips runs scripts/no-skips.sh on a go test -json stream and the given
// import paths, and returns its exit code and combined output.
func runNoSkips(t *testing.T, stream string, pkgs ...string) (int, string) {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatalf("jq is not on PATH; the gate needs it: %v", err)
	}
	path := filepath.Join(t.TempDir(), "tests.json")
	if err := os.WriteFile(path, []byte(stream), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(filepath.Join("..", "..", "scripts", "no-skips.sh"), append([]string{path}, pkgs...)...).CombinedOutput()
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
		{"a package-level pass with no test events (-run NoSuch)", ev("start", busPkg, "") + ev("output", busPkg, "") + ev("pass", busPkg, ""), 1, busPkg},
		{"only a sibling package's tests passed", ev("pass", sibling, "TestG"), 1, busPkg},
		{"the stream is not go test -json", "ok  \t" + busPkg + "\t1.0s\n", 2, ""},
		// One bus test passed and another failed. Under set +e or shell: bash {0},
		// go test's own exit status would no longer end the step, so the gate must
		// fail on the failure itself.
		{"one bus test passed and another failed", ran + ev("run", busPkg, "TestB") + ev("fail", busPkg, "TestB") + ev("fail", busPkg, ""), 1, "TestB"},
		{"a bus subtest failed", ran + ev("fail", busPkg, "TestA/sub"), 1, "TestA/sub"},
		{"a test under internal/bus failed", ran + ev("fail", busPkg+"/inner", "TestC"), 1, "TestC"},
		{"a failure in another package", ran + ev("fail", other, "TestD"), 0, ""},
		{"a failure in a package that only shares the prefix", ran + ev("fail", sibling, "TestE"), 0, ""},
		{"an empty stream", "", 2, ""},
		{"a package under internal/bus failed", ran + ev("fail", busPkg+"/inner", ""), 1, busPkg + "/inner"},
		// Measured on go1.27.0: the build events name the package in ImportPath and
		// carry no Package, then the package fails with FailedBuild set.
		{"the package failed to build, with its build events", `{"ImportPath":"` + busPkg + ` [` + busPkg + `.test]","Action":"build-output","Output":"bus.go:3:12: syntax error\n"}` + "\n" +
			`{"ImportPath":"` + busPkg + ` [` + busPkg + `.test]","Action":"build-fail"}` + "\n" +
			ev("start", busPkg, "") + `{"Action":"fail","Package":"` + busPkg + `","FailedBuild":"` + busPkg + ` [` + busPkg + `.test]"}` + "\n", 1, "syntax error"},
		{"the package failed to build", ev("start", busPkg, "") + ev("output", busPkg, "") + `{"Action":"fail","Package":"` + busPkg + `","FailedBuild":"` + busPkg + ` [` + busPkg + `.test]"}` + "\n", 1, "build"},
		// What go test -json writes for -run ^$, -skip . and -exec /usr/bin/true,
		// measured on go1.27.0: a start and a package pass, with no test events.
		{"-run ^$, -skip . or -exec /usr/bin/true", ev("start", busPkg, "") + ev("pass", busPkg, ""), 1, busPkg},
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

// The gate takes any number of import paths and holds each to the same rule,
// so one go test -json run of several packages needs one gate.
func TestNoSkipsGateEachPackage(t *testing.T) {
	t.Parallel()
	daemon := "github.com/arcavenae/marvel/internal/daemon"
	bus := ev("run", busPkg, "TestA") + ev("pass", busPkg, "TestA") + ev("pass", busPkg, "")
	dmn := ev("run", daemon, "TestD") + ev("pass", daemon, "TestD") + ev("pass", daemon, "")
	for _, tc := range []struct {
		name, stream string
		pkgs         []string
		want         int
		names        string
	}{
		{"both packages ran", bus + dmn, []string{busPkg, daemon}, 0, ""},
		{"one package has no passing test", bus + ev("pass", daemon, ""), []string{busPkg, daemon}, 1, daemon},
		{"one package skipped a test", bus + dmn + ev("skip", daemon, "TestE"), []string{busPkg, daemon}, 1, "TestE"},
		{"one package failed a test", bus + dmn + ev("fail", daemon, "TestE"), []string{busPkg, daemon}, 1, "TestE"},
		{"a package missing from the stream", bus, []string{busPkg, daemon}, 1, daemon},
		{"no import path", bus, nil, 2, "usage"},
		{"an empty import path", bus, []string{busPkg, ""}, 2, "usage"},
	} {
		got, out := runNoSkips(t, tc.stream, tc.pkgs...)
		switch {
		case got != tc.want:
			t.Errorf("%s: exit %d, want %d; output:\n%s", tc.name, got, tc.want, out)
		case tc.names != "" && !strings.Contains(out, tc.names):
			t.Errorf("%s: output does not name %s:\n%s", tc.name, tc.names, out)
		}
	}
}

func outEv(pkg, test, output string) string {
	if test == "" {
		return `{"Action":"output","Package":"` + pkg + `","Output":"` + output + `\n"}` + "\n"
	}
	return `{"Action":"output","Package":"` + pkg + `","Test":"` + test + `","Output":"` + output + `\n"}` + "\n"
}

// On a failure the gate prints what the failed tests and the failed package
// wrote, since the step sends go test's output to the stream and the job log
// would otherwise show only the test names. Output of a passing test, or of a
// failure in a package the gate was not asked about, stays out.
func TestNoSkipsGatePrintsFailedOutput(t *testing.T) {
	t.Parallel()
	other := "github.com/arcavenae/marvel/internal/daemon"
	stream := ev("run", busPkg, "TestA") + outEv(busPkg, "TestA", "chatter from a passing test") + ev("pass", busPkg, "TestA") +
		ev("run", busPkg, "TestB") + outEv(busPkg, "TestB", "bus_test.go:9: want 1, got 2") + ev("fail", busPkg, "TestB") +
		outEv(busPkg, "", "FAIL bus package summary") + ev("fail", busPkg, "") +
		ev("run", other, "TestD") + outEv(other, "TestD", "output of another package") + ev("fail", other, "TestD")
	got, out := runNoSkips(t, stream, busPkg)
	if got != 1 {
		t.Fatalf("exit %d, want 1; output:\n%s", got, out)
	}
	for _, want := range []string{"    bus_test.go:9: want 1, got 2", "    FAIL bus package summary"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not carry the failed output %q:\n%s", want, out)
		}
	}
	for _, not := range []string{"chatter from a passing test", "output of another package"} {
		if strings.Contains(out, not) {
			t.Errorf("output carries %q, which no failure in %s wrote:\n%s", not, busPkg, out)
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
	for _, gap := range busGateGaps(job) {
		t.Error(gap)
	}
}

// busGateGaps lists why the job's bus skip gate could not fail it: no gate
// step, a gate before the nats-server install, or an if or a continue-on-error
// that is not false on the job or the gate step, which would let it skip or
// stay green.
func busGateGaps(job ciJob) []string {
	var gaps []string
	for _, why := range mayNotFail(job.If, job.ContinueOnError) {
		gaps = append(gaps, "the job sets "+why)
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
		return append(gaps, "no quality-gate step runs ./internal/bus/... with go test -json and passes the stream to scripts/no-skips.sh")
	case nats < 0 || gated < nats:
		gaps = append(gaps, fmt.Sprintf("the bus skip gate (step %d) runs before nats-server is installed (step %d), so every broker test would skip", gated, nats))
	}
	if whys := mayNotFail(job.Steps[gated].If, job.Steps[gated].ContinueOnError); len(whys) > 0 {
		gaps = append(gaps, fmt.Sprintf("the bus skip gate step (step %d) sets %s", gated, strings.Join(whys, " and ")))
	}
	return gaps
}

func TestBusGateGaps(t *testing.T) {
	t.Parallel()
	install := ciJobStep{Name: "Install nats-server", Run: "install nats-server"}
	gate := ciJobStep{Name: "gate", Run: "set -euo pipefail\n" +
		`go test -json -count=1 ./internal/bus/... > "${RUNNER_TEMP}/bus.json"` + "\n" +
		`scripts/no-skips.sh "${RUNNER_TEMP}/bus.json" ` + busPkg}
	job := func(steps ...ciJobStep) ciJob { return ciJob{RunsOn: "ubuntu-24.04", Steps: steps} }
	withJob := func(j ciJob, f func(*ciJob)) ciJob { f(&j); return j }
	withStep := func(st ciJobStep, f func(*ciJobStep)) ciJobStep { f(&st); return st }
	good := job(install, gate)
	for _, tc := range []struct {
		name string
		job  ciJob
		gaps int
	}{
		{"install then gate", good, 0},
		{"no gate", job(install), 1},
		{"gate before the install", job(gate, install), 1},
		{"the job has an if", withJob(good, func(j *ciJob) { j.If = "false" }), 1},
		{"the job continues on error", withJob(good, func(j *ciJob) { j.ContinueOnError = true }), 1},
		{"continue-on-error false on the job", withJob(good, func(j *ciJob) { j.ContinueOnError = false }), 0},
		{"the gate step has an if", job(install, withStep(gate, func(st *ciJobStep) { st.If = "github.event_name == 'push'" })), 1},
		{"the gate step continues on error", job(install, withStep(gate, func(st *ciJobStep) { st.ContinueOnError = true })), 1},
		{"the gate step continues on an expression", job(install, withStep(gate, func(st *ciJobStep) { st.ContinueOnError = "${{ inputs.soft }}" })), 1},
	} {
		if got := busGateGaps(tc.job); len(got) != tc.gaps {
			t.Errorf("%s: gaps = %q, want %d", tc.name, got, tc.gaps)
		}
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
		{"|| true after the gate", test + "\n" + gate + " || true", false},
		{"; true after the gate", test + "\n" + gate + "; true", false},
		{"a command after the gate", test + "\n" + gate + "\necho done", false},
		{"a blank line and a comment after the gate", test + "\n" + gate + "\n\n# done", true},
		{"|| true on the test line", strings.Replace(test, " > ", " || true > ", 1) + "\n" + gate, false},
		{"the gate reads another file", test + "\n" + strings.Replace(gate, "bus.json", "old.json", 1), false},
		{"the test writes no file", strings.Replace(test, ` > "${RUNNER_TEMP}/bus.json"`, "", 1) + "\n" + gate, false},
		{"set +e before the test", "set +e\n" + test + "\n" + gate, true},
		// The gate takes only import paths of this module: a shell operator glued
		// to the last one, or a word that is not one, is not read as the gate.
		{"; true glued to the gate's last path", test + "\n" + gate + ";true", false},
		{"; true glued to an extra module path", test + "\n" + gate + " " + modulePath + "/internal/daemon;true", false},
		{"a word that is not an import path after the gate's path", test + "\n" + gate + " extra", false},
		// Only set and the go test line may come before the gate. A trap on EXIT
		// that exits 0, or a function named after the gate, would decide the
		// step's status while the gate still looks like its last command.
		{"a trap on EXIT that exits 0", "trap 'exit 0' EXIT\n" + test + "\n" + gate, false},
		{"a function named after the gate", "scripts/no-skips.sh() { return 0; }\n" + test + "\n" + gate, false},
		{"a function defined after set on one line", "set -e; scripts/no-skips.sh() { :; }\n" + test + "\n" + gate, false},
		{"a trap after the test line's redirect", test + " && trap 'exit 0' EXIT\n" + gate, false},
		{"another command before the test line", "echo starting\n" + test + "\n" + gate, false},
		{"a go command other than go test before it", "go env -w GOFLAGS=-skip=TestA\n" + test + "\n" + gate, false},
		{"the test line goes on past its redirect", strings.Replace(test, `"${RUNNER_TEMP}/bus.json"`, `"${RUNNER_TEMP}/bus.json" -count=2`, 1) + "\n" + gate, false},
		{"set -o pipefail before the test", "set -e -o pipefail\n" + test + "\n" + gate, true},
	} {
		if got := busSkipsGated(tc.run); got != tc.want {
			t.Errorf("%s: busSkipsGated = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// busSkipsGated reports whether a run script tests ./internal/bus/... under
// the skip gate, as gatedPackages reads it.
func busSkipsGated(run string) bool {
	return slices.Contains(gatedPackages(run, []string{"internal/bus"}, false), "internal/bus")
}

// gatedPackages returns which of pkgs (directories such as internal/bus) a run
// script tests under the skip gate. The script's last command that is not
// blank or a comment must be scripts/no-skips.sh <file> followed only by
// import paths of this module, so nothing after the gate can replace its exit
// status. A package counts when the gate names its import path and a go test
// line with -json (and -race, when race is set), with no || on it, names
// ./<pkg> or ./<pkg>/... and writes its stream with "> <file>", the gate's
// file. Lines continued with a trailing backslash are read as one.
//
// Every command before the gate must be set with option words only, or a go
// test line that ends at "> <file>" and carries no shell operator, so nothing
// before the gate can decide the step's status in its place: a trap on EXIT
// that exits 0, or a function named scripts/no-skips.sh, makes the run read as
// nothing gated.
//
// The shell's handling of go test's own exit status does not matter here:
// under set +e or shell: bash {0} the gate still reads the stream, and fails
// on a skip, a failed test or a package with no passing test.
func gatedPackages(run string, pkgs []string, race bool) []string {
	var cmds [][]string
	for _, line := range strings.Split(strings.ReplaceAll(run, "\\\n", " "), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cmds = append(cmds, strings.Fields(line))
	}
	if len(cmds) == 0 {
		return nil
	}
	gate := cmds[len(cmds)-1]
	if len(gate) < 3 || !strings.HasSuffix(gate[0], "scripts/no-skips.sh") {
		return nil
	}
	file, named := gate[1], gate[2:]
	for _, f := range cmds[:len(cmds)-1] {
		if !setLine(f) && !plainTestLine(f, file) {
			return nil
		}
	}
	for _, arg := range named {
		if !strings.HasPrefix(arg, modulePath+"/") || strings.ContainsAny(arg, ";|&<>$`()'\"\\") {
			return nil
		}
	}
	var out []string
	for _, pkg := range pkgs {
		if !slices.Contains(named, modulePath+"/"+pkg) {
			continue
		}
		for _, f := range cmds[:len(cmds)-1] {
			if testLineNames(f, pkg, race) {
				out = append(out, pkg)
				break
			}
		}
	}
	return out
}

// setLine reports whether the fields of one command are set with only option
// words, such as set -euo pipefail or set +e.
func setLine(fields []string) bool {
	if fields[0] != "set" {
		return false
	}
	for _, f := range fields[1:] {
		if !setWord.MatchString(f) {
			return false
		}
	}
	return true
}

var setWord = regexp.MustCompile(`^([-+][A-Za-z]+|[a-z]+)$`)

// plainTestLine reports whether the fields of one command are a go test run
// that ends with "> file" and carries no shell operator before it, so it runs
// nothing but go test.
func plainTestLine(fields []string, file string) bool {
	n := len(fields)
	if n < 4 || fields[0] != "go" || fields[1] != "test" || fields[n-2] != ">" || fields[n-1] != file {
		return false
	}
	for _, f := range fields[2 : n-2] {
		if strings.ContainsAny(f, ";|&<>()`{}") {
			return false
		}
	}
	return true
}

// testLineNames reports whether the fields of one command are a go test run
// of pkg with -json, and -race when race is set. plainTestLine has already
// held the line to ending at "> <file>" with no shell operator.
func testLineNames(fields []string, pkg string, race bool) bool {
	if len(fields) < 2 || fields[0] != "go" || fields[1] != "test" ||
		!slices.Contains(fields, "-json") || (race && !slices.Contains(fields, "-race")) {
		return false
	}
	return slices.Contains(fields, "./"+pkg) || slices.Contains(fields, "./"+pkg+"/...")
}
