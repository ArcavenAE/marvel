package homeguard

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestScrubbedEnvKeepsOnlyTheNamedVariables(t *testing.T) {
	environ := []string{
		"PATH=/usr/bin", "LANG=C", "TMPDIR=/tmp/x", "HOME=/real",
		"DIRECTOR_NATS_PASS=planted", "GH_TOKEN=planted", "FOO=a=b", "NOEQUALS", "=odd",
	}
	got := scrubbedEnv(environ, []string{"PATH", "LANG", "TMPDIR"})
	want := []string{"PATH=/usr/bin", "LANG=C", "TMPDIR=/tmp/x"}
	if !slices.Equal(got, want) {
		t.Errorf("scrubbedEnv = %q, want %q", got, want)
	}
	if got := scrubbedEnv(nil, keepEnv); len(got) != 0 {
		t.Errorf("scrubbedEnv(nil) = %q, want nothing", got)
	}
	// A value that holds an equals sign survives whole when its name is kept.
	if got := scrubbedEnv([]string{"LANG=a=b"}, []string{"LANG"}); !slices.Equal(got, []string{"LANG=a=b"}) {
		t.Errorf("scrubbedEnv kept %q", got)
	}
}

// plantedCredentials are set in the environment a child binary starts with. None
// is a real credential: the values only mark that they arrived.
var plantedCredentials = []string{
	"DIRECTOR_NATS_PASS", "DIRECTOR_TOKEN", "NATS_USER", "NATS_PASS", "BEADS_ACTOR",
	"MARVEL_SOCKET", "GH_TOKEN", "GITHUB_TOKEN", "SOME_OTHER_SECRET",
}

// TestChildReportsCredentials runs only as a child of
// TestRunDropsPlantedCredentialVariables.
func TestChildReportsCredentials(t *testing.T) {
	if childMode != "creds" {
		t.Skip("child only")
	}
	for _, name := range plantedCredentials {
		if _, ok := os.LookupEnv(name); ok {
			t.Errorf("SET:%s", name)
		}
	}
	for _, name := range keepEnv {
		if _, ok := os.LookupEnv(name); !ok {
			t.Errorf("MISSING:%s", name)
		}
	}
}

// A guarded test binary started with credential variables in its environment
// must not hand them to anything it starts. The child is the test binary
// itself, never a harness binary. The kept variables are the control: they
// arrive, so a child that sees nothing is not a child that saw an empty env.
func TestRunDropsPlantedCredentialVariables(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestChildReportsCredentials$", "-test.v")
	cmd.Env = []string{"HOMEGUARD_CHILD=creds", "HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH"), "LANG=C", "TMPDIR=" + os.TempDir()}
	for _, name := range plantedCredentials {
		cmd.Env = append(cmd.Env, name+"=planted")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("a planted credential variable reached a guarded binary: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "--- PASS: TestChildReportsCredentials") {
		t.Errorf("the child test did not run to a pass (a skip would hide a leak):\n%s", out)
	}
}
