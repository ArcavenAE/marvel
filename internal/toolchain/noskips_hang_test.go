package toolchain

import (
	"fmt"
	"strings"
	"testing"
)

// A test binary that passes -timeout writes "panic: test timed out", the
// running tests and a goroutine dump, and go test -json attributes those lines
// to the running test. No test then emits a fail event: only the package
// fails. The gate must print that panic, or a hung run names no test.
const hangPkg = "github.com/arcavenae/marvel/internal/hung"

func hangStream(extraGoroutineLines int) string {
	s := ev("run", hangPkg, "TestFast") + outEv(hangPkg, "TestFast", "chatter from a passing test") + ev("pass", hangPkg, "TestFast") +
		ev("run", hangPkg, "TestHangs") + ev("run", hangPkg, "TestHangs/sub") +
		outEv(hangPkg, "TestHangs/sub", "panic: test timed out after 10m0s") +
		outEv(hangPkg, "TestHangs/sub", `\trunning tests:`) +
		outEv(hangPkg, "TestHangs/sub", `\t\tTestHangs (10m0s)`) +
		outEv(hangPkg, "TestHangs/sub", `\t\tTestHangs/sub (10m0s)`)
	for i := 0; i < extraGoroutineLines; i++ {
		s += outEv(hangPkg, "TestHangs/sub", fmt.Sprintf("goroutine line %d", i))
	}
	return s + outEv(hangPkg, "", `FAIL\t`+hangPkg+`\t600.150s`) + ev("fail", hangPkg, "")
}

// The gate prints the timeout panic and the running tests of a package that
// failed with no failing test, so the job log names the hung test.
func TestNoSkipsGateNamesTheHungTest(t *testing.T) {
	t.Parallel()
	got, out := runNoSkips(t, hangStream(0), hangPkg)
	if got != 1 {
		t.Fatalf("exit %d, want 1; output:\n%s", got, out)
	}
	for _, want := range []string{"panic: test timed out after 10m0s", "running tests:", "TestHangs/sub (10m0s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not carry %q, so a hang names no test:\n%s", want, out)
		}
	}
	if strings.Contains(out, "chatter from a passing test") {
		t.Errorf("output carries a passing test's chatter:\n%s", out)
	}
}

// The goroutine dump of a big binary runs to thousands of lines, so the gate
// prints from the panic on and stops at a cap.
func TestNoSkipsGateCapsTheHangOutput(t *testing.T) {
	t.Parallel()
	got, out := runNoSkips(t, hangStream(500), hangPkg)
	if got != 1 {
		t.Fatalf("exit %d, want 1; output:\n%s", got, out)
	}
	if !strings.Contains(out, "TestHangs/sub (10m0s)") {
		t.Errorf("the cap cut the running tests:\n%s", out)
	}
	if strings.Contains(out, "goroutine line 499") {
		t.Errorf("output runs past the cap:\n%s", out)
	}
	if n := strings.Count(out, "\n"); n > 80 {
		t.Errorf("output is %d lines, want at most 80 for a capped hang", n)
	}
}

// With -parallel set high, go lists every running test, one line each, ahead
// of the goroutine dump. The gate prints that whole list, so the hung test's
// name cannot fall past the cap, and caps only the dump after it.
func TestNoSkipsGatePrintsTheWholeRunningTestsBlock(t *testing.T) {
	t.Parallel()
	const running = 100
	s := ev("run", hangPkg, "TestHangs") + outEv(hangPkg, "TestHangs", "panic: test timed out after 10m0s") +
		outEv(hangPkg, "TestHangs", `\trunning tests:`)
	for i := 0; i < running; i++ {
		s += outEv(hangPkg, "TestHangs", fmt.Sprintf(`\t\tTestHangs/sub%03d (10m0s)`, i))
	}
	s += outEv(hangPkg, "TestHangs", "")
	for i := 0; i < 500; i++ {
		s += outEv(hangPkg, "TestHangs", fmt.Sprintf("goroutine line %d", i))
	}
	s += outEv(hangPkg, "", `FAIL\t`+hangPkg+`\t600.150s`) + ev("fail", hangPkg, "")
	got, out := runNoSkips(t, s, hangPkg)
	if got != 1 {
		t.Fatalf("exit %d, want 1; output:\n%s", got, out)
	}
	for _, want := range []string{"TestHangs/sub000 (10m0s)", "TestHangs/sub099 (10m0s)", "goroutine line 0"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not carry %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "goroutine line 499") {
		t.Errorf("the goroutine dump runs past the cap:\n%s", out)
	}
}
