package upgrade

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBrew puts a brew on PATH that logs its arguments and exits with the
// codes given. It stands in for Homebrew, which these tests must not run.
func fakeBrew(t *testing.T, updateExit, upgradeExit int) (log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> '" + log + "'\n" +
		"case \"$1\" in\n" +
		"  update) exit " + itoa(updateExit) + " ;;\n" +
		"  upgrade) echo 'Error: the upgrade did not finish' >&2; exit " + itoa(upgradeExit) + " ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(dir, "brew"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	return string(rune('0' + n))
}

func captureOut(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := out
	out = &buf
	t.Cleanup(func() { out = prev })
	return &buf
}

// marvel#486. Measured on Homebrew 7.0.7: `brew upgrade` on a formula that is
// already current exits 0 with an "already installed" warning, and a real
// failure exits 1. A non-zero exit is therefore a failure, and the operator
// must be told so with a non-zero exit of marvel's own.
func TestFailedBrewUpgradeFails(t *testing.T) {
	fakeBrew(t, 0, 1)
	buf := captureOut(t)

	err := upgradeViaHomebrew("alpha")
	if err == nil {
		t.Fatalf("a failed brew upgrade returned nil; output:\n%s", buf)
	}
	if !strings.Contains(err.Error(), "brew upgrade") || !strings.Contains(err.Error(), "failed") {
		t.Errorf("error = %q, want it to say brew upgrade failed", err)
	}
	if strings.Contains(buf.String(), "Already up to date") || strings.Contains(buf.String(), "Upgrade complete") {
		t.Errorf("a failure was reported as success:\n%s", buf)
	}
}

func TestSuccessfulBrewUpgradeSucceeds(t *testing.T) {
	log := fakeBrew(t, 0, 0)
	captureOut(t)

	if err := upgradeViaHomebrew("alpha"); err != nil {
		t.Fatalf("a successful brew upgrade returned %v", err)
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "upgrade arcavenae/tap/marvel") {
		t.Errorf("brew was not asked to upgrade the formula; calls:\n%s", calls)
	}
}

func TestFailedBrewUpdateFails(t *testing.T) {
	log := fakeBrew(t, 1, 0)
	captureOut(t)

	err := upgradeViaHomebrew("alpha")
	if err == nil || !strings.Contains(err.Error(), "brew update failed") {
		t.Fatalf("error = %v, want brew update failed", err)
	}
	if calls, _ := os.ReadFile(log); strings.Contains(string(calls), "upgrade") {
		t.Errorf("brew upgrade ran after brew update failed:\n%s", calls)
	}
}

// A system package manager owns the binary, so marvel cannot upgrade it. That
// is not a success: the host is not upgraded, and a rollout script must see it.
func TestPackagePathFails(t *testing.T) {
	err := runMethod(methodPackage, "alpha", "")
	if err == nil {
		t.Fatal("the package path returned nil, so a host that was not upgraded reads as upgraded")
	}
	if !strings.Contains(err.Error(), "package manager") {
		t.Errorf("error = %q, want it to point at the package manager", err)
	}
}

func writeBinary(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestFingerprintFollowsTheContents(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	writeBinary(t, a, "build one")
	writeBinary(t, b, "build one")
	fa, err := fingerprint(a)
	if err != nil || fa == "" {
		t.Fatalf("fingerprint = %q, %v", fa, err)
	}
	if fb, _ := fingerprint(b); fb != fa {
		t.Errorf("identical contents fingerprint differently: %q and %q", fa, fb)
	}
	writeBinary(t, b, "build two")
	if fb, _ := fingerprint(b); fb == fa {
		t.Error("different contents fingerprint the same")
	}
	if _, err := fingerprint(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing binary fingerprinted without an error")
	}
}

// The daemon is re-executed only into a binary that changed. A symlink is
// followed, because that is how brew moves the active build.
func TestRunWithReportsWhetherTheBinaryChanged(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "marvel")
	writeBinary(t, exe, "build one")

	res, err := runWith(exe, func() error { return nil })
	if err != nil || res.Changed {
		t.Errorf("an upgrade that changed nothing: %+v, %v, want unchanged", res, err)
	}

	res, err = runWith(exe, func() error { writeBinary(t, exe, "build two"); return nil })
	if err != nil || !res.Changed {
		t.Errorf("an upgrade that replaced the binary: %+v, %v, want changed", res, err)
	}

	v1, v2 := filepath.Join(dir, "v1"), filepath.Join(dir, "v2")
	writeBinary(t, v1, "keg one")
	writeBinary(t, v2, "keg two")
	link := filepath.Join(dir, "linked")
	if err := os.Symlink(v1, link); err != nil {
		t.Fatal(err)
	}
	res, err = runWith(link, func() error {
		if err := os.Remove(link); err != nil {
			return err
		}
		return os.Symlink(v2, link)
	})
	if err != nil || !res.Changed {
		t.Errorf("a symlink moved to a new build: %+v, %v, want changed", res, err)
	}
}

func TestRunWithReturnsTheUpgradeError(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "marvel")
	writeBinary(t, exe, "build one")

	res, err := runWith(exe, func() error {
		writeBinary(t, exe, "build two")
		return os.ErrInvalid
	})
	if err == nil {
		t.Fatal("an upgrade error was swallowed")
	}
	if res.Changed {
		t.Error("a failed upgrade reported a changed binary, which would allow a re-exec")
	}
}

// If the installed binary cannot be read afterwards, nothing is known about it,
// so it is an error and the daemon is not re-executed.
func TestRunWithFailsWhenTheInstalledBinaryCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "marvel")
	writeBinary(t, exe, "build one")

	res, err := runWith(exe, func() error { return os.Remove(exe) })
	if err == nil || res.Changed {
		t.Errorf("a binary that vanished: %+v, %v, want an error and no change", res, err)
	}
}

// kegLayout builds the Homebrew layout on Linux in a temp dir: the version
// lives in Cellar/marvel/<v>, opt/marvel is the stable link brew retargets on
// upgrade, and `brew --prefix` answers with that link. The fake brew's upgrade
// installs 0.2, retargets the link, and with cleanup removes 0.1 the way brew's
// default does. It returns the path the running process would report as its
// own: on Linux os.Executable() is readlink(/proc/self/exe), the resolved keg.
func kegLayout(t *testing.T, cleanup bool) (self string) {
	t.Helper()
	root := t.TempDir()
	old := filepath.Join(root, "Cellar", "marvel", "0.1", "bin")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	self = filepath.Join(old, "marvel")
	writeBinary(t, self, "marvel 0.1")
	opt := filepath.Join(root, "opt", "marvel")
	if err := os.MkdirAll(filepath.Dir(opt), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "Cellar", "marvel", "0.1"), opt); err != nil {
		t.Fatal(err)
	}

	rm := ""
	if cleanup {
		rm = "rm -rf '" + filepath.Join(root, "Cellar", "marvel", "0.1") + "'\n"
	}
	bin := t.TempDir()
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  --prefix) echo '" + opt + "' ;;\n" +
		"  update) ;;\n" +
		"  upgrade)\n" +
		"    mkdir -p '" + filepath.Join(root, "Cellar", "marvel", "0.2", "bin") + "'\n" +
		"    echo 'marvel 0.2' > '" + filepath.Join(root, "Cellar", "marvel", "0.2", "bin", "marvel") + "'\n" +
		"    chmod +x '" + filepath.Join(root, "Cellar", "marvel", "0.2", "bin", "marvel") + "'\n" +
		"    ln -sfn '" + filepath.Join(root, "Cellar", "marvel", "0.2") + "' '" + opt + "'\n" +
		rm +
		"    ;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "brew"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return self
}

// The Linux case from the review of #498. The running process's own path is the
// old keg, which brew never retargets: without cleanup it still holds the old
// build, so the upgrade read as "did not change", and with cleanup (brew's
// default) it is gone, so a successful upgrade read as an error. The path that
// tracks what is installed is brew's stable link.
func TestHomebrewOnLinuxFingerprintsTheStableLink(t *testing.T) {
	for _, cleanup := range []bool{false, true} {
		name := "without cleanup"
		if cleanup {
			name = "with cleanup"
		}
		t.Run(name, func(t *testing.T) {
			self := kegLayout(t, cleanup)
			captureOut(t)

			res, err := runInstall(methodHomebrew, installedBinary(methodHomebrew, self), "alpha", "")
			if err != nil {
				t.Fatalf("a successful upgrade failed: %v", err)
			}
			if !res.Changed {
				t.Error("the upgrade installed 0.2 and was reported as unchanged")
			}
		})
	}
}

// Off Homebrew the running binary is the installed one, so nothing changes.
func TestInstalledBinaryIsTheRunningOneOffHomebrew(t *testing.T) {
	for _, m := range []installMethod{methodDirect, methodPackage} {
		if got := installedBinary(m, "/usr/local/bin/marvel"); got != "/usr/local/bin/marvel" {
			t.Errorf("method %d: installedBinary = %q", m, got)
		}
	}
}

// Without brew's answer there is no stable link to read, and the running
// binary is the best remaining guess (macOS reports the invoked symlink).
func TestInstalledBinaryFallsBackWhenBrewCannotSayWhere(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if got := installedBinary(methodHomebrew, "/opt/homebrew/bin/marvel"); got != "/opt/homebrew/bin/marvel" {
		t.Errorf("installedBinary = %q, want the running binary", got)
	}
}

// With no baseline there is nothing to lose: a binary that cannot be read
// before or after is not known to have changed, and that is not an error,
// because the upgrade itself succeeded.
func TestRunWithHasNoErrorWithoutABaseline(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "never-installed")

	res, err := runWith(exe, func() error { return nil })
	if err != nil || res.Changed {
		t.Errorf("got %+v, %v, want no change and no error", res, err)
	}
}

// brew can fail after it has relinked, leaving a new binary on disk. The
// operator is told, because the exit code alone says nothing was installed.
func TestFailedUpgradeThatChangedTheBinaryIsSaid(t *testing.T) {
	var buf bytes.Buffer
	prev := errOut
	errOut = &buf
	t.Cleanup(func() { errOut = prev })
	exe := filepath.Join(t.TempDir(), "marvel")
	writeBinary(t, exe, "build one")

	_, err := runWith(exe, func() error {
		writeBinary(t, exe, "build two")
		return os.ErrInvalid
	})
	if err == nil {
		t.Fatal("the failure was swallowed")
	}
	for _, want := range []string{"changed", "failure", exe} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("note %q lacks %q", buf.String(), want)
		}
	}

	buf.Reset()
	_, _ = runWith(exe, func() error { return os.ErrInvalid })
	if buf.Len() != 0 {
		t.Errorf("a failure that changed nothing printed %q", buf.String())
	}
}

// The daemon re-executes its OWN os.Executable, which on Linux is the resolved
// keg it started from. After a brew upgrade that keg is the old build or gone,
// so --daemon can never adopt the new one there, and exec after detach leaves
// the daemon not serving. The honest answer is to refuse before upgrading.
func TestReexecIsRefusedOnLinuxHomebrew(t *testing.T) {
	err := reexecRefusal("linux", methodHomebrew)
	if err == nil {
		t.Fatal("--daemon was allowed on a Linux Homebrew install")
	}
	for _, want := range []string{"Linux", "Homebrew", "Nothing was upgraded", "without --daemon", "marvel stop", "marvel daemon"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	for _, tc := range []struct {
		goos   string
		method installMethod
	}{
		{"darwin", methodHomebrew},
		{"linux", methodDirect},
		{"linux", methodPackage},
	} {
		if err := reexecRefusal(tc.goos, tc.method); err != nil {
			t.Errorf("%s method %d was refused: %v", tc.goos, tc.method, err)
		}
	}
}
