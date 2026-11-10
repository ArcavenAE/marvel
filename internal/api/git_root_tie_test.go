package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arcavenae/marvel/internal/gittest"
)

// A seat can write a .git file in a folder it owns. trusted_folders is the
// operator's list of repositories whose codex trust a seat may inherit, so a
// planted .git file must not make an unlisted folder read as part of a listed
// repository.

func plantGitFile(t *testing.T, folder, gitdir string) {
	t.Helper()
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGitMainRootRefusesAPlantedFileAtTheCommonDir(t *testing.T) {
	t.Parallel()
	repo := gittest.Repo(t, filepath.Join(t.TempDir(), "trusted"))
	folder := filepath.Join(t.TempDir(), "unlisted")
	plantGitFile(t, folder, filepath.Join(repo, ".git"))
	root, reason := GitMainRoot(folder)
	if root != "" || reason == "" {
		t.Fatalf("GitMainRoot(%s) = %q, %q: a .git file pointing at another repository's common dir read as part of it", folder, root, reason)
	}
}

func TestGitMainRootRefusesAPlantedFileAtALinkedWorktreesDir(t *testing.T) {
	t.Parallel()
	repo := gittest.Repo(t, filepath.Join(t.TempDir(), "trusted"))
	wt := gittest.Worktree(t, repo, "feat")
	folder := filepath.Join(t.TempDir(), "unlisted")
	plantGitFile(t, folder, filepath.Join(repo, ".git", "worktrees", filepath.Base(wt)))
	root, reason := GitMainRoot(folder)
	if root != "" || reason == "" {
		t.Fatalf("GitMainRoot(%s) = %q, %q: a .git file pointing at a worktree's admin dir read as that worktree", folder, root, reason)
	}
}

// The controls: the shapes a real checkout takes still give the main root, so a
// refusal above is the planted file and not a check that refuses everything.
func TestGitMainRootStillAcceptsRealCheckouts(t *testing.T) {
	t.Parallel()
	repo := gittest.Repo(t, filepath.Join(t.TempDir(), "trusted"))
	sub := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	wt := gittest.Worktree(t, repo, "feat")
	wtSub := filepath.Join(wt, "x")
	if err := os.MkdirAll(wtSub, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, start := range map[string]string{
		"main checkout":      repo,
		"main subdirectory":  sub,
		"linked worktree":    wt,
		"worktree subfolder": wtSub,
	} {
		root, reason := GitMainRoot(start)
		if root != repo || reason != "" {
			t.Errorf("%s: GitMainRoot(%s) = %q, %q, want %s", name, start, root, reason, repo)
		}
	}
}
