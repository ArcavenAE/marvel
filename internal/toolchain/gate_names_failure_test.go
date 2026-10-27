package toolchain

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// fakeGo stands in for go test -json: for each ./<pkg> argument it writes one
// failing test with output to the stream, then exits 1, as go test does when
// a test fails.
const fakeGo = `#!/bin/sh
for a in "$@"; do
  case "$a" in
    ./*)
      p="${a#./}"; p="${p%/...}"; pkg="github.com/arcavenae/marvel/$p"
      printf '{"Action":"run","Package":"%s","Test":"TestBroken"}\n' "$pkg"
      printf '{"Action":"output","Package":"%s","Test":"TestBroken","Output":"    x_test.go:1: the broker said no\\n"}\n' "$pkg"
      printf '{"Action":"fail","Package":"%s","Test":"TestBroken"}\n' "$pkg"
      printf '{"Action":"fail","Package":"%s"}\n' "$pkg"
      ;;
  esac
done
exit 1
`

// TestEveryGateNamesAFailingTest (marvel#1060): when a test fails, each CI
// step that ends in scripts/no-skips.sh still reaches the gate, which names
// the failing test and prints its output, and the step fails. Each step runs
// as GitHub runs a step with no shell, bash -e with pipefail, against a stub
// go that fails one test per package.
func TestEveryGateNamesAFailingTest(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatalf("jq is not on PATH; the gate needs it: %v", err)
	}
	jobs, err := ciJobs(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(fakeGo), 0o755); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(jobs))
	for name := range jobs {
		names = append(names, name)
	}
	sort.Strings(names)
	gates := 0
	for _, name := range names {
		for _, step := range jobs[name].Steps {
			if !strings.Contains(step.Run, "scripts/no-skips.sh") {
				continue
			}
			gates++
			if step.Shell != "" {
				t.Errorf("%s / %s: sets shell %q; this test runs GitHub's default only", name, step.Name, step.Shell)
				continue
			}
			script := filepath.Join(t.TempDir(), "step.sh")
			if err := os.WriteFile(script, []byte(step.Run), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "--noprofile", "--norc", "-eo", "pipefail", script)
			cmd.Dir = filepath.Join("..", "..")
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "RUNNER_TEMP="+t.TempDir())
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 {
				t.Errorf("%s / %s: exit %v, want 1; output:\n%s", name, step.Name, err, out)
			}
			for _, want := range []string{"TestBroken", "the broker said no"} {
				if !strings.Contains(string(out), want) {
					t.Errorf("%s / %s: output does not name %q, so a red run hides its test:\n%s", name, step.Name, want, out)
				}
			}
		}
	}
	if gates < 3 {
		t.Errorf("found %d gate steps in ci.yml, want at least 3 (bus, childproof, restructure)", gates)
	}
}
