package toolchain

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The stable signing job downloads the release archives, signs the darwin
// binaries inside them, and uploads the signed files. A job that finds nothing
// to sign must fail, not report success: a release would otherwise ship
// unsigned darwin binaries with every sign of a signed one. These tests run the
// job's own steps against stub tools and archives laid out the way goreleaser
// writes them (files at the archive root, no wrapping directory).

const signStubGH = `#!/bin/sh
# Stand-in for gh: "release download" copies the fixture archives into the
# working directory; "release upload" records its arguments.
case "$1 $2" in
"release download") cp "$FIXTURES"/*.tar.gz . ;;
"release upload") shift 3; printf '%s\n' "$@" | grep -v '^--' > "$SCRATCH/uploaded" ;;
*) echo "unexpected gh $*" >&2; exit 2 ;;
esac
`

const signStubLog = `#!/bin/sh
printf '%s %s\n' "$(basename "$0")" "$*" >> "$SCRATCH/calls"
`

// writeArchive writes a goreleaser-shaped archive: marvel, marvel-simulator,
// LICENSE and README.md at the root.
func writeArchive(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, name := range []string{"marvel", "marvel-simulator", "LICENSE", "README.md"} {
		body := []byte("x\n")
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []interface{ Close() error }{tw, gz, f} {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func signStep(t *testing.T, job ciJob, name string) string {
	t.Helper()
	for _, s := range job.Steps {
		if s.Name == name {
			if strings.Contains(s.Run, "${{") {
				t.Fatalf("step %q puts an expression in its script; pass it through env: and quote the variable", name)
			}
			return s.Run
		}
	}
	t.Fatalf("sign-release has no step named %q", name)
	return ""
}

// runSignSteps runs the download, sign and upload steps in one scratch
// directory, each as the runner's `bash -e` would, and returns each step's exit
// error, the scratch directory and the tool calls recorded.
func runSignSteps(t *testing.T, platforms []string) (errs map[string]error, scratch string) {
	t.Helper()
	jobs, err := ciJobs(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	job, ok := jobs["sign-release"]
	if !ok {
		t.Fatal("release.yml has no sign-release job")
	}
	scratch = t.TempDir()
	fixtures := filepath.Join(scratch, "fixtures")
	stubs := filepath.Join(scratch, "stubs")
	work := filepath.Join(scratch, "work")
	for _, d := range []string{fixtures, stubs, filepath.Join(work, "scripts")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range platforms {
		writeArchive(t, filepath.Join(fixtures, "marvel_1.0.0_"+p+".tar.gz"))
	}
	write := func(path, body string) {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(stubs, "gh"), signStubGH)
	write(filepath.Join(stubs, "codesign"), signStubLog)
	write(filepath.Join(stubs, "xcrun"), signStubLog)
	// The packaging scripts make the file they are asked for.
	write(filepath.Join(work, "scripts", "create-app.sh"), "#!/bin/sh\nmkdir -p \"$3/Marvel.app\"\n")
	write(filepath.Join(work, "scripts", "create-dmg.sh"), "#!/bin/sh\n: > \"$3\"\n")
	write(filepath.Join(work, "scripts", "create-pkg.sh"), "#!/bin/sh\n: > \"$4\"\n")

	env := append(os.Environ()[:0:0],
		"PATH="+stubs+":/usr/bin:/bin", "HOME="+scratch, "LANG=C",
		"SCRATCH="+scratch, "FIXTURES="+fixtures, "VERSION=1.0.0", "TAG=v1.0.0", "GH_TOKEN=unused",
		"APPLE_SIGNING_IDENTITY=id", "APPLE_INSTALLER_IDENTITY=id",
		"APPLE_NOTARIZATION_APPLE_ID=id", "APPLE_NOTARIZATION_PASSWORD=pw", "APPLE_NOTARIZATION_TEAM_ID=team")
	errs = map[string]error{}
	for _, name := range []string{"Download release archives", "Sign, package, and notarize", "Upload signed artifacts to release"} {
		cmd := exec.Command("/bin/bash", "-e", "-c", signStep(t, job, name))
		cmd.Dir = work
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		errs[name] = err
		if err != nil {
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("step %q did not run: %v", name, err)
			}
			t.Logf("step %q: %v\n%s", name, err, out)
		}
	}
	return errs, scratch
}

func TestSignJobSignsAndUploadsEveryDarwinArchive(t *testing.T) {
	t.Parallel()
	errs, scratch := runSignSteps(t, []string{"darwin_arm64", "darwin_amd64", "linux_amd64", "linux_arm64"})
	for name, err := range errs {
		if err != nil {
			t.Errorf("step %q failed on a normal release: %v", name, err)
		}
	}
	calls, _ := os.ReadFile(filepath.Join(scratch, "calls"))
	signed := 0
	for _, l := range strings.Split(string(calls), "\n") {
		if strings.HasPrefix(l, "codesign ") && strings.Contains(l, "--timestamp") && !strings.Contains(l, "Marvel.app") {
			signed++
		}
	}
	if signed != 2 {
		t.Errorf("signed %d darwin binaries, want 2 (arm64 and amd64):\n%s", signed, calls)
	}
	up, _ := os.ReadFile(filepath.Join(scratch, "uploaded"))
	got := strings.Fields(string(up))
	sort.Strings(got)
	want := []string{"marvel-amd64.dmg", "marvel-amd64.pkg", "marvel-arm64.dmg", "marvel-arm64.pkg", "marvel-darwin-amd64", "marvel-darwin-arm64"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("uploaded %v, want %v", got, want)
	}
}

func TestSignJobFailsWhenThereIsNothingToSign(t *testing.T) {
	t.Parallel()
	errs, _ := runSignSteps(t, []string{"linux_amd64", "linux_arm64"})
	if errs["Sign, package, and notarize"] == nil {
		t.Error("the sign step reported success having signed nothing")
	}
}

func TestSignJobUploadFailsOnNoAssets(t *testing.T) {
	t.Parallel()
	jobs, err := ciJobs(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	run := signStep(t, jobs["sign-release"], "Upload signed artifacts to release")
	scratch := t.TempDir()
	stubs := filepath.Join(scratch, "stubs")
	if err := os.MkdirAll(stubs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stubs, "gh"), []byte("#!/bin/sh\n: > \"$PWD/gh-was-called\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/bash", "-e", "-c", run)
	cmd.Dir = scratch
	cmd.Env = []string{"PATH=" + stubs + ":/usr/bin:/bin", "HOME=" + scratch, "GH_TOKEN=unused"}
	// The stub gh succeeds, so only the step's own guard can fail it.
	if err := cmd.Run(); err == nil {
		t.Error("the upload step reported success with no files to upload")
	}
	if _, err := os.Stat(filepath.Join(scratch, "gh-was-called")); err == nil {
		t.Error("the upload step called gh with nothing to upload")
	}
}
