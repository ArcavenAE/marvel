package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	buildOnce sync.Once
	binPath   string
	buildErr  error
)

// built compiles the command once and returns the binary, so the tests below
// see what an upgrade sees: an exit status and two streams, not a return value.
func built(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "rolescheck")
		if err != nil {
			buildErr = err
			return
		}
		binPath = filepath.Join(dir, "rolescheck")
		if out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput(); err != nil {
			buildErr = errors.New(string(out))
		}
	})
	if buildErr != nil {
		t.Fatalf("build: %v", buildErr)
	}
	return binPath
}

func exec1(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(built(t), args...)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		code = ee.ExitCode()
	default:
		t.Fatalf("run binary: %v", err)
	}
	return o.String(), e.String(), code
}

// On success the binary exits 0, prints the JSON object on stdout, and prints
// nothing on stderr.
func TestRolescheckBinarySuccess(t *testing.T) {
	out, errOut, code := exec1(t, fixture)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want empty", errOut)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got) != 2 {
		t.Errorf("stdout is not the two-team object: %v\n%s", err, out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("stdout %q does not end in a newline", out)
	}
}

// A manifest that does not read, parse or apply exits 1 with the error on
// stderr and nothing on stdout.
func TestRolescheckBinaryErrorExitsOneWithStderr(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.toml")
	if err := os.WriteFile(bad, []byte("[workspace\nname = "), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, arg := range []string{bad, "/no/such/manifest.toml"} {
		out, errOut, code := exec1(t, arg)
		if code != 1 {
			t.Errorf("%s: exit = %d, want 1", arg, code)
		}
		if out != "" {
			t.Errorf("%s: stdout = %q, want empty", arg, out)
		}
		if errOut == "" {
			t.Errorf("%s: stderr empty, want the error", arg)
		}
	}
}

// No manifest argument is a usage error: exit 2, usage on stderr, nothing on
// stdout. Arguments past the first are ignored, as the checker has always done.
func TestRolescheckBinaryUsageAndExtraArguments(t *testing.T) {
	out, errOut, code := exec1(t)
	if code != 2 {
		t.Errorf("no args: exit = %d, want 2", code)
	}
	if out != "" || !strings.Contains(errOut, "usage") {
		t.Errorf("no args: stdout %q stderr %q, want empty stdout and a usage line", out, errOut)
	}
	if _, errOut, code := exec1(t, fixture, "extra"); code != 0 {
		t.Errorf("extra arg: exit = %d, want 0 (stderr %q)", code, errOut)
	}
}

// The binary the tests build is removed when the test that asked for it ends,
// so repeated runs do not leave an executable behind in the temp directory.
func TestRolescheckBuiltBinaryIsRemovedWithItsTest(t *testing.T) {
	var path string
	t.Run("build", func(t *testing.T) {
		path = built(t)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("binary missing while its test runs: %v", err)
		}
	})
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("binary %s still exists after its test ended (stat err %v)", path, err)
	}
}
