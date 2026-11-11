package toolchain

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A tag name is chosen by whoever pushes the tag, and it can carry shell
// characters. A run script that has the tag substituted into its text runs
// that spelling as code beside the signing identity, so the release workflow
// hands the tag to its scripts through env and the scripts quote it.
func TestReleaseScriptsDoNotInterpolateTheTag(t *testing.T) {
	t.Parallel()
	jobs, err := ciJobs(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) == 0 {
		t.Fatal("release.yml has no jobs")
	}
	for name, job := range jobs {
		for _, s := range job.Steps {
			for _, expr := range []string{"github.ref_name", "github.ref ", "github.head_ref"} {
				if strings.Contains(s.Run, "${{ "+expr) {
					t.Errorf("job %s step %q substitutes ${{ %s }} into its script", name, s.Name, strings.TrimSpace(expr))
				}
			}
		}
	}
}

// runCreateApp runs a copy of scripts/create-app.sh inside a scratch tree, so
// it writes only there, and returns its error, its output and whether the
// plist was written.
func runCreateApp(t *testing.T, version string) (out string, wrote bool, err error) {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"scripts", "packaging"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"scripts/create-app.sh", "packaging/Info.plist"} {
		b, err := os.ReadFile(filepath.Join("..", "..", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, f), b, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	binary := filepath.Join(root, "marvel")
	if err := os.WriteFile(binary, []byte("x\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(root, "out")
	cmd := exec.Command("/bin/bash", filepath.Join(root, "scripts", "create-app.sh"), binary, version, outDir)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + root, "LANG=C"}
	b, runErr := cmd.CombinedOutput()
	_, statErr := os.Stat(filepath.Join(outDir, "Marvel.app", "Contents", "Info.plist"))
	return string(b), statErr == nil, runErr
}

func TestCreateAppRefusesAVersionThatIsNotPlain(t *testing.T) {
	t.Parallel()
	for _, v := range []string{
		"1.0.0/../x",
		"1.0.0;touch x",
		"1.0.0 2",
		"1.0.0&",
		"1.0.0\"",
		"$(id)",
		"1.0.0\n2",
	} {
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			out, wrote, err := runCreateApp(t, v)
			if err == nil {
				t.Fatalf("create-app.sh accepted version %q: %s", v, out)
			}
			if wrote {
				t.Errorf("create-app.sh wrote a plist for version %q", v)
			}
			if !strings.Contains(out, "version has characters outside") {
				t.Errorf("output %q does not give the reason", out)
			}
		})
	}
}

func TestCreateAppAcceptsAPlainVersion(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"1.0.0", "0.1.0-alpha.20261111.012502+35b0b96", "2.3.4-rc.1"} {
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			out, wrote, err := runCreateApp(t, v)
			if err != nil {
				t.Fatalf("create-app.sh refused version %q: %v\n%s", v, err, out)
			}
			if !wrote {
				t.Errorf("create-app.sh wrote no plist for version %q", v)
			}
		})
	}
}
