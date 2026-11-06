package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// marvel#1315: under the shipped sandbox profile a goose seat may write only to
// its private home and its working directory, so a temp file under the daemon's
// inherited per-user temp directory is refused and goose panics at start. The
// adapter points TMPDIR at a directory under the seat's home when a profile is
// in force. These tests cover TMPDIR only; no run under the profile was made.

const gooseProfile = "/etc/marvel/goose.sb"

func gooseAuto(rt *api.Runtime) {
	headless(rt)
	rt.Goose.Posture = api.GoosePostureAuto
}

func prepareGooseEnv(t *testing.T, ctx *LaunchContext) map[string]string {
	t.Helper()
	res, err := (&Goose{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return res.Env
}

// A profile in force and an absolute home: TMPDIR is a directory under the home,
// for an interactive and a headless seat alike.
func TestGooseTMPDIRIsUnderTheSeatHomeWhenAProfileIsInForce(t *testing.T) {
	for name, mutate := range map[string]func(*api.Runtime){"interactive": nil, "headless": gooseAuto} {
		t.Run(name, func(t *testing.T) {
			ctx := gooseContext(t, mutate)
			ctx.SandboxProfile = gooseProfile
			want := filepath.Join(ctx.HarnessHomePath, "tmp")
			if got := prepareGooseEnv(t, ctx)["TMPDIR"]; got != want {
				t.Errorf("TMPDIR = %q, want %q", got, want)
			}
		})
	}
}

// No profile: the seat keeps whatever temp directory it inherits, so marvel adds
// no TMPDIR and an inherited one is not copied into the map.
func TestGooseSetsNoTMPDIRWithoutAProfile(t *testing.T) {
	ctx := gooseContext(t, gooseAuto)
	ctx.Placement = api.PlacementContainer
	plain := gooseContext(t, nil)
	t.Setenv("TMPDIR", "/inherited/tmp") // after the contexts, which make temp dirs
	if got, ok := prepareGooseEnv(t, ctx)["TMPDIR"]; ok {
		t.Errorf("TMPDIR = %q, want none without a sandbox profile", got)
	}
	if got, ok := prepareGooseEnv(t, plain)["TMPDIR"]; ok {
		t.Errorf("interactive TMPDIR = %q, want none without a sandbox profile", got)
	}
}

// A relative home gives no safe place to name: no TMPDIR, even with a profile.
func TestGooseSetsNoTMPDIRForARelativeHome(t *testing.T) {
	ctx := gooseContext(t, nil)
	ctx.SandboxProfile = gooseProfile
	ctx.HarnessHomePath = "relative/home"
	if got, ok := prepareGooseEnv(t, ctx)["TMPDIR"]; ok {
		t.Errorf("TMPDIR = %q, want none for a relative home", got)
	}
}

// The role's own TMPDIR wins, and so does the inherited process value never
// count as the role's: the daemon's TMPDIR is exactly the value to replace.
func TestGooseRoleTMPDIRWinsAndTheInheritedOneDoesNot(t *testing.T) {
	ctx := gooseContext(t, func(rt *api.Runtime) { rt.Env = map[string]string{"TMPDIR": "/role/chose/this"} })
	ctx.SandboxProfile = gooseProfile
	own := gooseContext(t, nil)
	own.SandboxProfile = gooseProfile
	t.Setenv("TMPDIR", "/var/folders/inherited") // after the contexts, which make temp dirs
	if got := prepareGooseEnv(t, ctx)["TMPDIR"]; got != "/role/chose/this" {
		t.Errorf("TMPDIR = %q, want the role's value", got)
	}
	if got := prepareGooseEnv(t, own)["TMPDIR"]; got == "/var/folders/inherited" || got == "" {
		t.Errorf("TMPDIR = %q, want the seat's own directory, not the inherited one", got)
	}
}

func seedHome(t *testing.T, ctx *LaunchContext, dir string) error {
	t.Helper()
	spec, ok := (&Goose{}).SessionHome(ctx)
	if !ok || spec.Seed == nil {
		t.Fatal("goose offers no seed")
	}
	return spec.Seed(dir)
}

// The seeder makes the directory, 0700, once a profile is in force, and leaves
// it alone on a second seed; it makes none without a profile.
func TestGooseSeederMakesTheTempDirOnlyUnderAProfile(t *testing.T) {
	ctx := gooseContext(t, nil)
	ctx.SandboxProfile = gooseProfile
	dir := t.TempDir()
	if err := seedHome(t, ctx, dir); err != nil {
		t.Fatalf("seed: %v", err)
	}
	st, err := os.Stat(filepath.Join(dir, "tmp"))
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
		t.Fatalf("tmp dir = %v, %v; want a 0700 directory", st, err)
	}
	marker := filepath.Join(dir, "tmp", "keep")
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := seedHome(t, ctx, dir); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("a second seed cleared the temp dir: %v", err)
	}

	plain := gooseContext(t, nil)
	other := t.TempDir()
	if err := seedHome(t, plain, other); err != nil {
		t.Fatalf("seed without a profile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(other, "tmp")); err == nil {
		t.Error("a seat with no profile got a temp dir")
	}
}

// A directory that cannot be made fails the seed, as the config directory does.
func TestGooseSeedFailsWhenTheTempDirCannotBeMade(t *testing.T) {
	ctx := gooseContext(t, nil)
	ctx.SandboxProfile = gooseProfile
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tmp"), []byte("a file in the way"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := seedHome(t, ctx, dir); err == nil {
		t.Fatal("seed succeeded with a file where the temp dir goes")
	}
}

// The directory is named only for an absolute home. Prepare refuses a relative
// home before it reaches this, so the rule is pinned at the function both the
// launch env and the seeder ask.
func TestGooseTempDirNeedsAnAbsoluteHome(t *testing.T) {
	ctx := gooseContext(t, nil)
	ctx.SandboxProfile = gooseProfile
	if _, ok := gooseTempDir(ctx); !ok {
		t.Fatal("no temp dir for an absolute home under a profile")
	}
	ctx.HarnessHomePath = "relative/home"
	if dir, ok := gooseTempDir(ctx); ok {
		t.Errorf("temp dir %q named for a relative home", dir)
	}
}
