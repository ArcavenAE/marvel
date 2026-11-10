package runtime

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// The seeders write fixed-name files into a seat's kept home. A seat can plant
// a link there between launches, so a write that follows it lands outside the
// home, past whatever confinement the sandbox profile sets up around it. These
// tests plant the link and check that the write stays inside the home or is
// refused.

const outsideContent = "outside file the seat does not own"

// outsideTarget is a file outside the home, holding content the seeder must
// not change.
func outsideTarget(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(p, []byte(outsideContent), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func requireUntouched(t *testing.T, path string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != outsideContent {
		t.Fatalf("the write reached %s: it now holds %q", path, got)
	}
}

func seedGooseInto(t *testing.T, dir string, mutate func(ctx *LaunchContext)) error {
	t.Helper()
	ctx := gooseContext(t, nil)
	if mutate != nil {
		mutate(ctx)
	}
	spec, ok := (&Goose{}).SessionHome(ctx)
	if !ok || spec.Seed == nil {
		t.Fatalf("SessionHome = %+v, %v", spec, ok)
	}
	return spec.Seed(dir)
}

// A link planted at the temp name must not carry the write: the temp file is
// created exclusively under a name the seat cannot predict.
func TestWriteSeedFileIgnoresALinkAtTheOldTempName(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	target := outsideTarget(t)
	path := filepath.Join(home, "config.toml")
	if err := os.Symlink(target, path+".marvel-tmp"); err != nil {
		t.Fatal(err)
	}
	if err := writeSeedFile(path, codexSeed{HookCommand: "marvel codex-ctx"}); err != nil {
		t.Fatalf("writeSeedFile: %v", err)
	}
	requireUntouched(t, target)
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("the seed is not a regular file: %v %v", fi, err)
	}
}

// A link at the final path is refused, not replaced, so the seat's planted
// entry is surfaced.
func TestWriteSeedFileRefusesALinkAtTheFinalPath(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	target := outsideTarget(t)
	path := filepath.Join(home, "config.toml")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := writeSeedFile(path, codexSeed{HookCommand: "marvel codex-ctx"}); err == nil {
		t.Fatal("writeSeedFile replaced a symlink at the final path")
	}
	requireUntouched(t, target)
}

func TestWriteSeedFileLeavesNoTempFileBehindOnRefusal(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(home, "config.toml")
	if err := os.Symlink(outsideTarget(t), path); err != nil {
		t.Fatal(err)
	}
	_ = writeSeedFile(path, codexSeed{HookCommand: "marvel codex-ctx"})
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("home holds %d entries after a refused write, want only the planted link", len(entries))
	}
}

func TestGooseSeedRefusesALinkedConfigDir(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(home, "config")); err != nil {
		t.Fatal(err)
	}
	if err := seedGooseInto(t, home, nil); err == nil {
		t.Fatal("Seed accepted a config directory that is a link")
	}
	entries, err := os.ReadDir(elsewhere)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the seed reached the link target: %d entries written", len(entries))
	}
}

func TestGooseSeedRefusesALinkedTempDir(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(home, "tmp")); err != nil {
		t.Fatal(err)
	}
	err := seedGooseInto(t, home, func(ctx *LaunchContext) { ctx.SandboxProfile = gooseProfile })
	if err == nil {
		t.Fatal("Seed accepted a temp directory that is a link")
	}
}

func TestGooseSeedRefusesALinkedYAMLFile(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"config.yaml", "permission.yaml"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			target := outsideTarget(t)
			cfg := filepath.Join(home, "config")
			if err := os.MkdirAll(cfg, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(cfg, name)); err != nil {
				t.Fatal(err)
			}
			if err := seedGooseInto(t, home, nil); err == nil {
				t.Fatalf("Seed wrote through a link at %s", name)
			}
			requireUntouched(t, target)
		})
	}
}

// A hard link to a file outside the home has link count two: the open would
// truncate the other file's inode, so it is refused before any write.
func TestGooseSeedRefusesAHardLinkedYAMLFile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	target := outsideTarget(t)
	cfg := filepath.Join(home, "config")
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, filepath.Join(cfg, "config.yaml")); err != nil {
		t.Skipf("no hard links here: %v", err)
	}
	if err := seedGooseInto(t, home, nil); err == nil {
		t.Fatal("Seed wrote through a hard link")
	}
	requireUntouched(t, target)
}

// The ordinary relaunch still works: the seed is rewritten in place.
func TestGooseSeedRewritesItsOwnFilesOnRelaunch(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	for i := 0; i < 2; i++ {
		if err := seedGooseInto(t, home, nil); err != nil {
			t.Fatalf("launch %d: %v", i, err)
		}
	}
	for _, name := range []string{"config.yaml", "permission.yaml"} {
		fi, err := os.Lstat(filepath.Join(home, "config", name))
		if err != nil || !fi.Mode().IsRegular() {
			t.Fatalf("%s: %v %v", name, fi, err)
		}
	}
}

// A FIFO planted at a seed path must be refused, not waited on: an open of a
// FIFO for writing blocks until a reader appears, which would hang the launch.
func TestGooseSeedRefusesAFIFOAtASeedPath(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cfg := filepath.Join(home, "config")
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(cfg, "config.yaml"), 0o600); err != nil {
		t.Skipf("no FIFOs here: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- seedGooseInto(t, home, nil) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Seed accepted a FIFO at a seed path")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Seed blocked on a FIFO at a seed path")
	}
}

// The codex seed is written to a temp file and renamed over the final path, so
// a reader never sees half of it and an existing hard link to the old file is
// not rewritten. A write straight to the final path keeps the old inode and
// fails this test.
func TestWriteSeedFileReplacesTheFinalPathByRename(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(home, "config.toml")
	if err := os.WriteFile(path, []byte("old seed"), 0o600); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(home, "old-name")
	if err := os.Link(path, keep); err != nil {
		t.Skipf("no hard links here: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeSeedFile(path, codexSeed{HookCommand: "marvel codex-ctx"}); err != nil {
		t.Fatalf("writeSeedFile: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Fatal("the final path is the same file as before: the write did not go through a rename")
	}
	if got := string(readFile(t, keep)); got != "old seed" {
		t.Fatalf("the old file changed to %q: the write went to it directly", got)
	}
}
