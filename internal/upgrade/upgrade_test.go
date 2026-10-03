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
