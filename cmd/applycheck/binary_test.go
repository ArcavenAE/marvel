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
		dir, err := os.MkdirTemp("", "applycheck")
		if err != nil {
			buildErr = err
			return
		}
		binPath = filepath.Join(dir, "applycheck")
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
func TestApplycheckBinarySuccess(t *testing.T) {
	out, errOut, code := exec1(t, fixture, "/srv/root")
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

// On a manifest that does not parse or apply the binary panics: a nonzero exit
// with nothing on stdout, which the upgrade reads as failure. A main that
// printed the error and exited 0 would pass an empty stdout as a good check.
func TestApplycheckBinaryFailsNonzeroWithEmptyStdout(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.toml")
	if err := os.WriteFile(bad, []byte("[workspace\nname = "), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{bad, "/srv/root"}, {"/no/such/manifest.toml", "/srv/root"}, {fixture}, {}} {
		out, errOut, code := exec1(t, args...)
		if code == 0 {
			t.Errorf("%v: exit 0, want nonzero", args)
		}
		if out != "" {
			t.Errorf("%v: stdout = %q, want empty", args, out)
		}
		if errOut == "" {
			t.Errorf("%v: stderr empty, want the panic text", args)
		}
	}
}

// Arguments past the second are ignored, as the checker has always done.
func TestApplycheckBinaryIgnoresExtraArguments(t *testing.T) {
	_, errOut, code := exec1(t, fixture, "/srv/root", "extra", "more")
	if code != 0 {
		t.Errorf("exit = %d, want 0 with extra arguments (stderr %q)", code, errOut)
	}
}
