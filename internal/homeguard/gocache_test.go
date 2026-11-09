package homeguard

import (
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func fakeGoEnv(t *testing.T, out string, err error) *[]string {
	t.Helper()
	var got []string
	orig := runGoEnv
	runGoEnv = func(env []string) ([]byte, error) {
		got = append([]string{}, env...)
		if got == nil {
			got = []string{}
		}
		return []byte(out), err
	}
	t.Cleanup(func() { runGoEnv = orig })
	return &got
}

func TestResolveGoCaches(t *testing.T) {
	cases := []struct {
		name     string
		environ  []string
		goOut    string
		goErr    error
		wantRun  bool
		want     []string
		wantNote string
	}{
		{"both set, go is not asked", []string{"GOCACHE=/c", "GOMODCACHE=/m"}, "", nil, false, []string{"GOCACHE=/c", "GOMODCACHE=/m"}, ""},
		{"neither set, go answers", []string{"HOME=/real"}, "/gc\n/gm\n", nil, true, []string{"GOCACHE=/gc", "GOMODCACHE=/gm"}, ""},
		{"one set, the other from go", []string{"GOMODCACHE=/m"}, "/gc\n/gm-from-go\n", nil, true, []string{"GOCACHE=/gc", "GOMODCACHE=/m"}, ""},
		{"an empty value counts as unset", []string{"GOCACHE=", "GOMODCACHE=/m"}, "/gc\n/gm\n", nil, true, []string{"GOCACHE=/gc", "GOMODCACHE=/m"}, ""},
		{"go missing", []string{"GOMODCACHE=/m"}, "", errors.New("executable file not found"), true, []string{"GOMODCACHE=/m"}, "GOCACHE left unset"},
		{"go missing, neither set", nil, "", errors.New("not found"), true, nil, "GOCACHE and GOMODCACHE left unset"},
		{"go prints the wrong number of lines", nil, "/gc\n", nil, true, nil, "GOCACHE and GOMODCACHE left unset"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			seen := fakeGoEnv(t, c.goOut, c.goErr)
			got, note := resolveGoCaches(c.environ)
			if ran := *seen != nil; ran != c.wantRun {
				t.Errorf("go env ran = %v, want %v", ran, c.wantRun)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("entries = %q, want %q", got, c.want)
			}
			if (c.wantNote == "") != (note == "") || !strings.Contains(note, c.wantNote) {
				t.Errorf("note = %q, want it to contain %q", note, c.wantNote)
			}
		})
	}
}

// The one `go env` call is given the real HOME and PATH and no credential
// variable, so resolving the caches does not hand a credential to go.
func TestResolveGoCachesGivesGoNoCredentialVariable(t *testing.T) {
	seen := fakeGoEnv(t, "/gc\n/gm\n", nil)
	resolveGoCaches([]string{"PATH=/bin", "HOME=/real", "GOPATH=/gp", "DIRECTOR_NATS_PASS=planted", "GH_TOKEN=planted", "NATS_PASS=planted"})
	want := []string{"PATH=/bin", "HOME=/real", "GOPATH=/gp"}
	if !slices.Equal(*seen, want) {
		t.Errorf("go env was given %q, want %q", *seen, want)
	}
}

// TestChildReportsGoCaches runs only as a child of
// TestChildGoCachesAreTheResolvedOnesNotUnderTheScratchHome and
// TestGoMissingLeavesTheCachesUnsetAndSaysSo.
func TestChildReportsGoCaches(t *testing.T) {
	switch childMode {
	case "gocache":
		out, err := exec.Command("go", "env", "GOMODCACHE", "GOCACHE").Output()
		if err != nil {
			t.Fatalf("go env: %v", err)
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		t.Logf("CHILD:HOME=%s", os.Getenv("HOME"))
		t.Logf("CHILD:GOMODCACHE=%s", lines[0])
		t.Logf("CHILD:GOCACHE=%s", lines[1])
	case "nogo":
		for _, name := range goCacheNames {
			if v, ok := os.LookupEnv(name); ok {
				t.Errorf("%s is set to %q with no go to resolve it", name, v)
			}
		}
	default:
		t.Skip("child only")
	}
}

func childValue(out, name string) string {
	for _, line := range strings.Split(out, "\n") {
		if _, v, ok := strings.Cut(line, "CHILD:"+name+"="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// A child of a guarded binary that runs `go env` finds the caches the parent
// resolved, not an empty pair under the scratch HOME. The child is started the
// way a shell starts it: the real HOME and no go variables.
func TestChildGoCachesAreTheResolvedOnesNotUnderTheScratchHome(t *testing.T) {
	wantMod, wantCache := os.Getenv("GOMODCACHE"), os.Getenv("GOCACHE")
	if wantMod == "" || wantCache == "" {
		t.Fatalf("the guard left GOMODCACHE=%q GOCACHE=%q unset in this binary", wantMod, wantCache)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestChildReportsGoCaches$", "-test.v")
	cmd.Env = []string{"HOMEGUARD_CHILD=gocache", "HOME=" + realHome, "PATH=" + os.Getenv("PATH"), "TMPDIR=" + os.TempDir()}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	o := string(out)
	childHome := childValue(o, "HOME")
	if childHome == "" || !strings.Contains(childHome, "marvel-test-home-") {
		t.Fatalf("the child did not report its scratch HOME %q:\n%s", childHome, o)
	}
	for name, want := range map[string]string{"GOMODCACHE": wantMod, "GOCACHE": wantCache} {
		got := childValue(o, name)
		if got != want {
			t.Errorf("child go env %s = %q, want the parent's resolved %q", name, got, want)
		}
		if strings.HasPrefix(got, childHome) {
			t.Errorf("child go env %s = %q is under the scratch home %q", name, got, childHome)
		}
	}
}

func TestGoMissingLeavesTheCachesUnsetAndSaysSo(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestChildReportsGoCaches$", "-test.v")
	cmd.Env = []string{"HOMEGUARD_CHILD=nogo", "HOME=" + realHome, "PATH=" + t.TempDir(), "TMPDIR=" + os.TempDir()}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("a guarded binary with no go on PATH did not run its tests: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "homeguard: go env did not answer") || !strings.Contains(string(out), "GOCACHE and GOMODCACHE left unset") {
		t.Errorf("the guard did not say it left the caches unset:\n%s", out)
	}
	if !strings.Contains(string(out), "--- PASS: TestChildReportsGoCaches") {
		t.Errorf("the child test did not run to a pass:\n%s", out)
	}
}
