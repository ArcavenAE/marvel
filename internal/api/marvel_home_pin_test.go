package api

import (
	"path/filepath"
	"testing"

	"github.com/arcavenae/marvel/internal/gittest"
)

// MARVEL_HOME moves marvel's own state. It does not move what marvel reads from
// the user's real home. These pin the two such reads in this package, so a
// change that routed either through the layout would fail here.
// Not parallel: they set HOME and MARVEL_HOME.

func TestMarvelHomeDoesNotMoveATrustedFoldersTilde(t *testing.T) {
	home, other := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MARVEL_HOME", other)
	gittest.Repo(t, filepath.Join(home, "work", "repo-a"))
	if err := trustManifest(t, `trusted_folders = ["~/work/repo-a"]`).ValidateTrustedFolders(); err != nil {
		t.Errorf("~/ no longer resolves against the user's home: %v", err)
	}
	// The negative: a checkout that exists only under the root is not found.
	gittest.Repo(t, filepath.Join(other, "work", "repo-b"))
	if err := trustManifest(t, `trusted_folders = ["~/work/repo-b"]`).ValidateTrustedFolders(); err == nil {
		t.Errorf("~/ resolved against MARVEL_HOME")
	}
}

func TestMarvelHomeDoesNotMoveTheHarnessDefaultHome(t *testing.T) {
	home, other := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MARVEL_HOME", other)
	for _, tc := range []struct{ harness, dir string }{{"claude", ".claude"}, {"codex", ".codex"}} {
		if got := canonicalConfigHome(tc.harness, filepath.Join(home, tc.dir)); got != "" {
			t.Errorf("%s: the user's default %s reads as %q, want the default account", tc.harness, tc.dir, got)
		}
		if got := canonicalConfigHome(tc.harness, filepath.Join(other, tc.dir)); got == "" {
			t.Errorf("%s: a %s under MARVEL_HOME reads as the default account", tc.harness, tc.dir)
		}
	}
}
