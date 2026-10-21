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
	for _, gap := range linuxGateGaps(jobs) {
		t.Error(gap)
	}
}

// linuxGateGaps lists why the workflow's quality-gate job could not gate
// internal/bus skips: it is missing, it does not run on linuxRunner, or
// busGateGaps finds a gap in it.
func linuxGateGaps(jobs map[string]ciJob) []string {
	job, ok := jobs["quality-gate"]
	if !ok {
		return []string{"ci.yml has no quality-gate job"}
	}
	if !linuxRunner(job.RunsOn) {
		return []string{fmt.Sprintf("quality-gate runs on %q, want ubuntu", job.RunsOn)}
	}
	return busGateGaps(job)
}

// linuxRunner reports whether a runs-on label is the Linux runner the bus gate
// is held to.
func linuxRunner(label string) bool { return strings.HasPrefix(label, "ubuntu") }

// busGateGaps lists why the job's bus skip gate could not fail it: no gate
// step, a gate before the nats-server install, an if or a continue-on-error
// that is not false on the job or the gate step, which would let it skip or
// stay green, or a gate step whose shell bashShell does not accept or whose
// env, its job's or the workflow's sets a name shellEnv reports, or a job or
// gate step that sets a key the guard does not read (ExtraKeys).
func busGateGaps(job ciJob) []string {
	var gaps []string
	for _, why := range mayNotFail(job.If, job.ContinueOnError) {
		gaps = append(gaps, "the job sets "+why)
	}
	if len(job.ExtraKeys) > 0 {
		gaps = append(gaps, "the job sets "+strings.Join(job.ExtraKeys, " and ")+", which the guard does not read")
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
	if sh := stepShell(job, job.Steps[gated]); !bashShell(sh) {
		gaps = append(gaps, fmt.Sprintf("the bus skip gate step (step %d) runs under shell %q, which may not run the script", gated, sh))
	}
	if keys := job.Steps[gated].ExtraKeys; len(keys) > 0 {
		gaps = append(gaps, fmt.Sprintf("the bus skip gate step (step %d) sets %s, which the guard does not read", gated, strings.Join(keys, " and ")))
	}
	if names := shellEnv(job, job.Steps[gated]); len(names) > 0 {
		gaps = append(gaps, fmt.Sprintf("the bus skip gate step (step %d) sets %s in its env, its job's or the workflow's, outside the allowed names %v", gated, strings.Join(names, " and "), allowedEnv))
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
		// The step's shell runs the script. Anything but bash, such as
		// shell: true {0}, runs none of it and exits 0.
		{"the gate step's shell is true {0}", job(install, withStep(gate, func(st *ciJobStep) { st.Shell = "true {0}" })), 1},
		{"the gate step's shell is sh", job(install, withStep(gate, func(st *ciJobStep) { st.Shell = "sh" })), 1},
		{"the gate step's shell is bash -n {0}", job(install, withStep(gate, func(st *ciJobStep) { st.Shell = "bash -n {0}" })), 1},
		{"the gate step's shell is bash -c true {0}", job(install, withStep(gate, func(st *ciJobStep) { st.Shell = "bash -c true {0}" })), 1},
		{"the gate step's shell is bash -e with no {0}", job(install, withStep(gate, func(st *ciJobStep) { st.Shell = "bash -e" })), 1},
		{"the job's default shell is true {0}", withJob(good, func(j *ciJob) { j.Defaults.Run.Shell = "true {0}" }), 1},
		{"the gate step's shell is bash", job(install, withStep(gate, func(st *ciJobStep) { st.Shell = "bash" })), 0},
		{"the gate step's shell is bash {0}", job(install, withStep(gate, func(st *ciJobStep) { st.Shell = "bash {0}" })), 0},
		{"the gate step's shell is bash -e {0}", job(install, withStep(gate, func(st *ciJobStep) { st.Shell = "bash -e {0}" })), 0},
		{"the gate step's shell is GitHub's bash default", job(install, withStep(gate, func(st *ciJobStep) { st.Shell = "bash --noprofile --norc -eo pipefail {0}" })), 0},
		{"a bash step under a true {0} job default", withJob(job(install, withStep(gate, func(st *ciJobStep) { st.Shell = "bash" })), func(j *ciJob) { j.Defaults.Run.Shell = "true {0}" }), 0},
		// bash reads SHELLOPTS, BASH_ENV and BASHOPTS from the environment at
		// startup: SHELLOPTS=noexec runs nothing, and BASH_ENV can run a file
		// that exits 0 first. env: on the step, the job or the workflow sets them.
		{"the gate step sets SHELLOPTS", job(install, withStep(gate, func(st *ciJobStep) { st.Env = map[string]string{"SHELLOPTS": "noexec"} })), 1},
		{"the gate step sets BASH_ENV", job(install, withStep(gate, func(st *ciJobStep) { st.Env = map[string]string{"BASH_ENV": "scripts/exit0.sh"} })), 1},
		{"the gate step sets BASHOPTS", job(install, withStep(gate, func(st *ciJobStep) { st.Env = map[string]string{"BASHOPTS": "extdebug"} })), 1},
		{"the job sets SHELLOPTS", withJob(good, func(j *ciJob) { j.Env = map[string]string{"SHELLOPTS": "noexec"} }), 1},
		{"the job sets BASH_ENV", withJob(good, func(j *ciJob) { j.Env = map[string]string{"BASH_ENV": "scripts/exit0.sh"} }), 1},
		// Only the names ci.yml uses today are allowed; every other name is a
		// gap, so a new route through the environment fails closed.
		{"the gate step sets a name outside the allowlist", job(install, withStep(gate, func(st *ciJobStep) { st.Env = map[string]string{"GOFLAGS": "-mod=readonly"} })), 1},
		{"the gate step imports a go function (BASH_FUNC_go%%)", job(install, withStep(gate, func(st *ciJobStep) { st.Env = map[string]string{"BASH_FUNC_go%%": "() { :; }"} })), 1},
		{"the gate step sets PATH", job(install, withStep(gate, func(st *ciJobStep) { st.Env = map[string]string{"PATH": "./fake:/usr/bin"} })), 1},
		{"the job sets PATH", withJob(good, func(j *ciJob) { j.Env = map[string]string{"PATH": "./fake:/usr/bin"} }), 1},
		{"the job imports a jq function", withJob(good, func(j *ciJob) { j.Env = map[string]string{"BASH_FUNC_jq%%": "() { :; }"} }), 1},
		{"the gate step sets an allowed name", job(install, withStep(gate, func(st *ciJobStep) { st.Env = map[string]string{"NATS_SERVER_VERSION": "v2.15.0"} })), 0},
		// Only the job and step keys the guard reads are allowed: a key it does
		// not read, such as container:, can change the step's environment or
		// its tools. ciJobs fills ExtraKeys from the file.
		{"the job sets a key the guard does not read", withJob(good, func(j *ciJob) { j.ExtraKeys = []string{"container"} }), 1},
		{"the gate step sets a key the guard does not read", job(install, withStep(gate, func(st *ciJobStep) { st.ExtraKeys = []string{"working-directory"} })), 1},
		{"another step sets a key the guard does not read", job(withStep(install, func(st *ciJobStep) { st.ExtraKeys = []string{"id"} }), gate), 0},
		{"the job sets the allowed names", withJob(good, func(j *ciJob) {
			j.Env = map[string]string{"HOMEBREW_NO_AUTO_UPDATE": "1", "NATS_SERVER_SHA256": "abc", "NATS_SERVER_VERSION": "v2.15.0"}
		}), 0},
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
		{"a go command other than go test, with the stream redirect", `go env -w GOFLAGS=-skip=TestA > "${RUNNER_TEMP}/bus.json"` + "\n" + test + "\n" + gate, false},
		{"the test line goes on past its redirect", strings.Replace(test, `"${RUNNER_TEMP}/bus.json"`, `"${RUNNER_TEMP}/bus.json" -count=2`, 1) + "\n" + gate, false},
		{"set -o pipefail before the test", "set -e -o pipefail\n" + test + "\n" + gate, true},
		// set may only turn on or off errexit, nounset, pipefail and xtrace:
		// -n or -o noexec before the test line runs nothing, and the step exits 0.
		{"set -n before the test", "set -n\n" + test + "\n" + gate, false},
		{"set -o noexec before the test", "set -o noexec\n" + test + "\n" + gate, false},
		{"set +o noexec before the test", "set +o noexec\n" + test + "\n" + gate, false},
		// Either sign is allowed for the four options: the shipped macOS step
		// opens with set +e, so the gate still runs after a failed go test, and
		// the gate's own status, as the last command, is the step's.
		{"set +e, as the shipped macOS step opens", "set +e\n" + test + "\n" + gate, true},
		{"set +o errexit before the test", "set +o errexit\n" + test + "\n" + gate, true},
		{"set +eux and +o pipefail", "set +eux +o pipefail\n" + test + "\n" + gate, true},
		{"-n inside a cluster", "set -eun\n" + test + "\n" + gate, false},
		{"an unknown letter", "set -eq\n" + test + "\n" + gate, false},
		{"an unknown long name", "set -o posix\n" + test + "\n" + gate, false},
		{"-o with no name", "set -o\n" + test + "\n" + gate, false},
		{"o before another letter in a cluster", "set -oe pipefail\n" + test + "\n" + gate, false},
		{"a long name with no -o", "set pipefail\n" + test + "\n" + gate, false},
		// The gate is the last command: set +e after it ends the script with
		// status 0 whatever the gate found.
		{"set +e after the gate", test + "\n" + gate + "\nset +e", false},
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
	pre, gate := cmds[:len(cmds)-1], cmds[len(cmds)-1]
	if len(gate) < 3 || !strings.HasSuffix(gate[0], "scripts/no-skips.sh") {
		return nil
	}
	file, named := gate[1], gate[2:]
	for _, f := range pre {
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
		for _, f := range pre {
			if testLineNames(f, pkg, race) {
				out = append(out, pkg)
				break
			}
		}
	}
	return out
}

// setLine reports whether the fields of one command are set with only the
// options setOptions allows, such as set -euo pipefail or set +e.
func setLine(fields []string) bool {
	return fields[0] == "set" && setOptions(fields[1:])
}

// setOptions reports whether words only turn errexit, nounset, pipefail and
// xtrace on or off: -e, -u, -x, their + forms and clusters of them, and -o or
// +o followed by one of the four names, with o last in its cluster. Anything
// else, such as -n or -o noexec, which run nothing, is refused.
func setOptions(words []string) bool {
	for i := 0; i < len(words); i++ {
		w := words[i]
		if !setLetters.MatchString(w) {
			return false
		}
		switch strings.Count(w, "o") {
		case 0:
			continue
		case 1:
			if !strings.HasSuffix(w, "o") || i+1 >= len(words) || !slices.Contains(setNames, words[i+1]) {
				return false
			}
			i++
		default:
			return false
		}
	}
	return true
}

var (
	setLetters = regexp.MustCompile(`^[-+][euxo]+$`)
	setNames   = []string{"errexit", "nounset", "pipefail", "xtrace"}
)

// bashShell reports whether a step's shell runs its script with bash: empty
// (the runner's default, bash on the Linux and macOS images), bash, or bash
// with --noprofile, --norc and the options setOptions allows, then {0}. Any
// other shell, such as true {0}, may run none of the script.
func bashShell(shell string) bool {
	f := strings.Fields(shell)
	switch {
	case len(f) == 0 || len(f) == 1 && f[0] == "bash":
		return true
	case f[0] != "bash" || f[len(f)-1] != "{0}":
		return false
	}
	var opts []string
	for _, w := range f[1 : len(f)-1] {
		if w != "--noprofile" && w != "--norc" {
			opts = append(opts, w)
		}
	}
	return setOptions(opts)
}

// shellEnv names the variables set in the step's env or the job's (which
// ciJobs merges with the workflow's) that are not on allowedEnv, sorted. bash
// and the commands the gate runs read the environment in more ways than a
// denylist keeps up with: SHELLOPTS=noexec runs nothing, BASH_ENV sources a
// file first, BASH_FUNC_go%% replaces go with a function, and PATH can put a
// fake go or jq first. So every name but the few ci.yml uses is refused.
//
// Out of reach here: a value an earlier step writes to $GITHUB_ENV or a
// directory it adds to $GITHUB_PATH, since both are set at run time and not in
// the workflow file.
func shellEnv(job ciJob, step ciJobStep) []string {
	var names []string
	for _, env := range []map[string]string{step.Env, job.Env} {
		for name := range env {
			if !slices.Contains(allowedEnv, name) && !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	slices.Sort(names)
	return names
}

// allowedEnv is every env name ci.yml sets on a step the guards read, its job
// or the workflow, as of this change. A new name needs adding here, which puts
// it in front of a reviewer.
var allowedEnv = []string{"HOMEBREW_NO_AUTO_UPDATE", "NATS_SERVER_SHA256", "NATS_SERVER_VERSION"}

// stepShell is the shell a step's script runs under: its own, else the job's
// default, which ciJobs fills from the workflow's when the job names none.
func stepShell(job ciJob, step ciJobStep) string {
	if step.Shell != "" {
		return step.Shell
	}
	return job.Defaults.Run.Shell
}

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

// testLineNames reports whether the fields of one command before the gate
// name pkg with -json, and -race when race is set. setLine and plainTestLine
// have already held the command to set with option words, which cannot name a
// package, or a go test line ending at "> <file>" with no shell operator.
func testLineNames(fields []string, pkg string, race bool) bool {
	if !slices.Contains(fields, "-json") || (race && !slices.Contains(fields, "-race")) {
		return false
	}
	return slices.Contains(fields, "./"+pkg) || slices.Contains(fields, "./"+pkg+"/...")
}
