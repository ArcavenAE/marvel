package main

import (
	"os"
	"path/filepath"
	"testing"
)

func evalDir(t *testing.T, p string) string {
	t.Helper()
	got, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// The anchor is the absolute directory of the manifest FILE, never the CLI's
// working directory (marvel#255 decisions 2 and 3).
func TestWorkspaceRootForAnchorsOnTheManifestFile(t *testing.T) {
	tmp := evalDir(t, t.TempDir())
	projDir := filepath.Join(tmp, "a", "x")
	elsewhere := filepath.Join(tmp, "b", "y")
	for _, d := range []string{projDir, elsewhere} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	manifest := filepath.Join(projDir, "m.yaml")
	if err := os.WriteFile(manifest, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	// The case that must not differ: marvel work ../../a/x/m.yaml, run from
	// somewhere else entirely.
	t.Chdir(elsewhere)
	rel, err := filepath.Rel(elsewhere, manifest)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, path, declared, want string
	}{
		{"no declared root is the manifest's directory", manifest, "", projDir},
		{"the same from another working directory by a relative path", rel, "", projDir},
		{"a relative declared root joins the manifest's directory", manifest, "../shared", filepath.Join(tmp, "a", "shared")},
		{"a relative declared root joins it from another working directory too", rel, "../shared", filepath.Join(tmp, "a", "shared")},
		{"an absolute declared root wins", manifest, "/srv/proj", "/srv/proj"},
		{"a tilde root is left for the daemon to refuse", manifest, "~/proj", ""},
	}
	for _, tc := range cases {
		if got := workspaceRootFor(tc.path, tc.declared); got != tc.want {
			t.Errorf("%s: workspaceRootFor(%q, %q) = %q, want %q", tc.name, tc.path, tc.declared, got, tc.want)
		}
	}
}
