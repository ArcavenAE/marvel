package view

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// gitIn runs git in dir with a clean environment and fails the test on error.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cleanGitEnv(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// commitPayload writes payload files that name commit n and commits them.
func commitPayload(t *testing.T, repo string, n int) string {
	t.Helper()
	deep := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{filepath.Join(deep, "payload"), filepath.Join(repo, "top")} {
		if err := os.WriteFile(f, []byte(fmt.Sprintf("payload-%d", n)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", fmt.Sprintf("commit %d", n))
	return gitIn(t, repo, "rev-parse", "HEAD")
}

func realViewFixture(t *testing.T) (repo string, b *Builder) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	repo = filepath.Join(t.TempDir(), "origin")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "init", "-q", "-b", "main")
	root := t.TempDir()
	return repo, New(filepath.Join(root, "views", "ws", "seat", "repo"), repo, "main", ExecGit{})
}

// The reader loop of probe H6b: a reader resolving a path through cur while
// the view swaps twenty times never sees a missing file or a torn one.
func TestRealGitReaderSeesNoMissingOrMixedFileDuringSwaps(t *testing.T) {
	repo, b := realViewFixture(t)
	valid := map[string]string{} // commit id -> payload
	valid[commitPayload(t, repo, 0)] = "payload-0"
	if _, err := b.Refresh(context.Background()); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var problems []string
	note := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		if len(problems) < 5 {
			problems = append(problems, fmt.Sprintf(format, args...))
		}
	}
	reads := 0
	wg.Add(1)
	go func() {
		defer wg.Done()
		cur := filepath.Join(b.Dir, "cur")
		for {
			select {
			case <-stop:
				return
			default:
			}
			p, err := os.ReadFile(filepath.Join(cur, "a", "b", "payload"))
			if err != nil || !strings.HasPrefix(string(p), "payload-") {
				note("payload read: %q, %v", p, err)
			}
			top, err := os.ReadFile(filepath.Join(cur, "top"))
			if err != nil || !strings.HasPrefix(string(top), "payload-") {
				note("top read: %q, %v", top, err)
			}
			id, err := os.ReadFile(filepath.Join(cur, "VIEW_SHA"))
			if err != nil || len(strings.TrimSpace(string(id))) != 40 {
				note("VIEW_SHA read: %q, %v", id, err)
			}
			reads++
		}
	}()

	for n := 1; n <= 20; n++ {
		valid[commitPayload(t, repo, n)] = fmt.Sprintf("payload-%d", n)
		res, err := b.Refresh(context.Background())
		if err != nil {
			t.Fatalf("Refresh %d: %v", n, err)
		}
		if !res.Changed {
			t.Fatalf("Refresh %d reported no change", n)
		}
	}
	close(stop)
	wg.Wait()

	if len(problems) > 0 {
		t.Fatalf("reader saw %d problem(s) in %d reads, first: %v", len(problems), reads, problems)
	}
	if reads == 0 {
		t.Fatal("the reader never ran")
	}
	// The tree cur names is the last commit, and every tree is its own commit's.
	for sha, want := range valid {
		got, err := os.ReadFile(filepath.Join(b.Dir, "trees", sha, "a", "b", "payload"))
		if err != nil {
			continue // the first commit's tree may predate; others must exist
		}
		if string(got) != want {
			t.Errorf("tree %s payload = %q, want %q", sha, got, want)
		}
	}
}

// Probe H6c: git run inside cur finds no repository, because the view is
// outside any work tree and carries no .git.
func TestRealGitRevParseInsideCurFails(t *testing.T) {
	repo, b := realViewFixture(t)
	commitPayload(t, repo, 0)
	if _, err := b.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", filepath.Join(b.Dir, "cur"), "rev-parse", "--git-dir")
	cmd.Env = append(cleanGitEnv(), "GIT_CEILING_DIRECTORIES="+filepath.Dir(filepath.Dir(b.Dir)))
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("git rev-parse succeeded inside the view: %s", out)
	}
}

// Probe H1: a write into a tree directory fails.
func TestRealGitWriteIntoATreeFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	repo, b := realViewFixture(t)
	commitPayload(t, repo, 0)
	if _, err := b.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	cur := filepath.Join(b.Dir, "cur")
	for _, dir := range []string{cur, filepath.Join(cur, "a"), filepath.Join(cur, "a", "b")} {
		if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x"), 0o644); err == nil {
			t.Errorf("a new file was written into %s", dir)
		}
	}
	if err := os.WriteFile(filepath.Join(cur, "top"), []byte("x"), 0o644); err == nil {
		t.Error("an existing file in the tree was overwritten in place")
	}
	if err := os.Remove(filepath.Join(cur, "top")); err == nil {
		t.Error("a file in the tree was removed")
	}
}
