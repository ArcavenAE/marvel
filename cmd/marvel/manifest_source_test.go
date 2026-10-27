package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The source is the host name cut at the first dot and lowercased, then the
// manifest's absolute path with symlinks resolved, so one file reached through
// a link is one source.
func TestManifestSourceIsHostAndResolvedPath(t *testing.T) {
	dir := t.TempDir()
	realDir := filepath.Join(dir, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(realDir, "marvel.toml")
	if err := os.WriteFile(file, []byte("[workspace]\nname = \"w\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(file)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ host, path, want string }{
		{"Host-A.local", file, "host-a:" + resolved},
		{"host-a.lan", filepath.Join(link, "marvel.toml"), "host-a:" + resolved},
		{"HOSTB", file, "hostb:" + resolved},
	} {
		got, err := sourceOf(tc.host, tc.path)
		if err != nil {
			t.Errorf("sourceOf(%q, %q): %v", tc.host, tc.path, err)
			continue
		}
		if got != tc.want {
			t.Errorf("sourceOf(%q, %q) = %q, want %q", tc.host, tc.path, got, tc.want)
		}
	}
}

// A source that cannot be computed is an error, so marvel work sends none and
// the teams take nil claims, which are never pruned.
func TestManifestSourceRefusesWhatItCannotName(t *testing.T) {
	for _, tc := range []struct{ name, host, path string }{
		{"empty host", "", "marvel.toml"},
		{"dot only host", ".local", "marvel.toml"},
		{"missing file", "host-a", filepath.Join(t.TempDir(), "absent.toml")},
	} {
		if got, err := sourceOf(tc.host, tc.path); err == nil {
			t.Errorf("%s: sourceOf = %q, want an error", tc.name, got)
		}
	}
}
