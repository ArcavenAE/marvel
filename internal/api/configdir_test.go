package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalConfigDirIsOneSpellingPerLogin(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	real := filepath.Join(home, "acct-one")
	other := filepath.Join(home, "acct-two")
	for _, d := range []string{real, other, filepath.Join(home, ".claude")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(home, "link-one")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	defLink := filepath.Join(home, "link-default")
	if err := os.Symlink(filepath.Join(home, ".claude"), defLink); err != nil {
		t.Fatal(err)
	}

	one := CanonicalConfigDir(real, home)
	if one == "" {
		t.Fatal("a non-default directory canonicalized to the default")
	}
	for _, spelling := range []string{real + "/", real + "//", link, filepath.Join(home, ".", "acct-one"), filepath.Join(home, "acct-two", "..", "acct-one")} {
		if got := CanonicalConfigDir(spelling, home); got != one {
			t.Errorf("%q = %q, want %q", spelling, got, one)
		}
	}
	for _, spelling := range []string{"", filepath.Join(home, ".claude"), filepath.Join(home, ".claude") + "/", defLink} {
		if got := CanonicalConfigDir(spelling, home); got != "" {
			t.Errorf("%q = %q, want the default \"\"", spelling, got)
		}
	}
	if CanonicalConfigDir(other, home) == one {
		t.Error("two different directories share a label")
	}
	// A literal ~ is not expanded: it is its own spelling, never merged.
	if got := CanonicalConfigDir("~/acct-one", home); got == one || got == "" {
		t.Errorf("a literal ~ was resolved or defaulted: %q", got)
	}
	if got := CanonicalConfigDir("~/.claude", home); got == "" {
		t.Error("a literal ~/.claude was treated as the default")
	}
	// A directory that does not exist keeps its cleaned spelling.
	if got := CanonicalConfigDir("/no/such/dir/", home); got != "/no/such/dir" {
		t.Errorf("missing dir = %q", got)
	}
}
