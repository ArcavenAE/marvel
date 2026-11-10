package upgrade

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The upgrade writes fixed-name files into the install directory, which can be
// shared and writable. A link planted at one of those names must not carry the
// write to its target, and the failure advice must not send the operator to
// sudo, which would make that write a root-level one.

const outsideBytes = "a file the install directory does not own"

func outsideFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(p, []byte(outsideBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func requireOutsideUntouched(t *testing.T, p string) {
	t.Helper()
	if got := readFile(t, p); got != outsideBytes {
		t.Fatalf("the write reached %s: it now holds %q", p, got)
	}
}

func TestCheckWritableIgnoresALinkAtTheProbeName(t *testing.T) {
	dir := t.TempDir()
	target := outsideFile(t)
	if err := os.Symlink(target, filepath.Join(dir, ".marvel-update-test")); err != nil {
		t.Fatal(err)
	}
	if err := checkWritable(dir); err != nil {
		t.Fatalf("checkWritable: %v", err)
	}
	requireOutsideUntouched(t, target)
}

func TestCheckWritableLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	if err := checkWritable(dir); err != nil {
		t.Fatalf("checkWritable: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the probe left %d entries in the install directory", len(entries))
	}
}

func TestUnwritableInstallDirectoryDoesNotAdviseSudo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop root")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	err := checkWritable(dir)
	if err == nil {
		t.Fatal("an unwritable directory passed the check")
	}
	if strings.Contains(err.Error(), "sudo") {
		t.Errorf("the failure advises sudo: %v", err)
	}
	if !strings.Contains(err.Error(), "install to a directory you own, or use your package manager") {
		t.Errorf("the failure does not carry the replacement advice: %v", err)
	}
}

func TestInstallDirectRefusesALinkAtTheNewName(t *testing.T) {
	l, bin := directLayout(t)
	target := outsideFile(t)
	if err := os.Symlink(target, bin+".new"); err != nil {
		t.Fatal(err)
	}
	if err := installDirect(l, bin, []byte("build two"), "v2", "v1", false); err == nil {
		t.Fatal("installDirect wrote through a link at the .new name")
	}
	requireOutsideUntouched(t, target)
	if got := readFile(t, bin); got != "build one" {
		t.Fatalf("the running binary changed to %q", got)
	}
}

func TestInstallDirectRefusesALinkAtTheOldName(t *testing.T) {
	l, bin := directLayout(t)
	target := outsideFile(t)
	if err := os.Symlink(target, bin+".old"); err != nil {
		t.Fatal(err)
	}
	if err := installDirect(l, bin, []byte("build two"), "v2", "v1", false); err == nil {
		t.Fatal("installDirect accepted a link at the .old name")
	}
	requireOutsideUntouched(t, target)
	if got := readFile(t, bin); got != "build one" {
		t.Fatalf("the running binary changed to %q", got)
	}
	if _, err := os.Lstat(bin + ".new"); err == nil {
		t.Error("a .new file was left behind by the refusal")
	}
}

// A regular .new left by an upgrade that failed part way does not block the
// next one: it is removed and the file is created fresh.
func TestInstallDirectReplacesAStaleNewFile(t *testing.T) {
	l, bin := directLayout(t)
	writeBinary(t, bin+".new", "half written")
	if err := installDirect(l, bin, []byte("build two"), "v2", "v1", false); err != nil {
		t.Fatalf("installDirect: %v", err)
	}
	if got := readFile(t, bin); got != "build two" {
		t.Fatalf("the binary holds %q, want build two", got)
	}
}

func TestCopyFileRefusesALinkAtTheDestination(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(src, []byte("prior build"), 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "marvel")
	target := outsideFile(t)
	if err := os.Symlink(target, dst); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err == nil {
		t.Fatal("copyFile wrote through a link at the destination")
	}
	requireOutsideUntouched(t, target)
}

// The create is exclusive, so something planted after the check and before the
// open makes the open fail and is not followed or truncated. clearForCreate
// refuses a link first, so these tests plant in the window after it, through
// the afterClear seam.

func plantAfterClear(t *testing.T, plant func(path string)) {
	t.Helper()
	afterClear = plant
	t.Cleanup(func() { afterClear = nil })
}

func TestInstallDirectDoesNotFollowALinkPlantedAfterTheCheck(t *testing.T) {
	l, bin := directLayout(t)
	target := outsideFile(t)
	plantAfterClear(t, func(path string) {
		if err := os.Symlink(target, path); err != nil {
			t.Error(err)
		}
	})
	if err := installDirect(l, bin, []byte("build two"), "v2", "v1", false); err == nil {
		t.Fatal("installDirect wrote through a link planted after the check")
	}
	requireOutsideUntouched(t, target)
	if got := readFile(t, bin); got != "build one" {
		t.Fatalf("the running binary changed to %q", got)
	}
}

func TestInstallDirectDoesNotTruncateAFilePlantedAfterTheCheck(t *testing.T) {
	l, bin := directLayout(t)
	planted := ""
	plantAfterClear(t, func(path string) {
		planted = path
		if err := os.WriteFile(path, []byte("planted"), 0o600); err != nil {
			t.Error(err)
		}
	})
	if err := installDirect(l, bin, []byte("build two"), "v2", "v1", false); err == nil {
		t.Fatal("installDirect reused a file planted after the check")
	}
	if got := readFile(t, planted); got != "planted" {
		t.Fatalf("the planted file was rewritten to %q", got)
	}
}

func TestCopyFileDoesNotFollowALinkPlantedAfterTheCheck(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(src, []byte("prior build"), 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "marvel")
	target := outsideFile(t)
	plantAfterClear(t, func(path string) {
		if err := os.Symlink(target, path); err != nil {
			t.Error(err)
		}
	})
	if err := copyFile(src, dst); err == nil {
		t.Fatal("copyFile wrote through a link planted after the check")
	}
	requireOutsideUntouched(t, target)
}

func TestCopyFileDoesNotTruncateAFilePlantedAfterTheCheck(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(src, []byte("prior build"), 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "marvel")
	plantAfterClear(t, func(path string) {
		if err := os.WriteFile(path, []byte("planted"), 0o600); err != nil {
			t.Error(err)
		}
	})
	if err := copyFile(src, dst); err == nil {
		t.Fatal("copyFile reused a file planted after the check")
	}
	if got := readFile(t, dst); got != "planted" {
		t.Fatalf("the planted file was rewritten to %q", got)
	}
}
