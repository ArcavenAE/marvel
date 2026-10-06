package view

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
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
	b = New(filepath.Join(root, "views", "ws", "seat", "repo"), repo, "main", ExecGit{})
	// The trees are read-only; restore write so TempDir can remove them.
	t.Cleanup(func() { _ = forceRemove(b.Dir) })
	return repo, b
}

// The reader loop of probe H6b: a reader resolving a path through cur while
// the view swaps twenty times never sees a torn or wrong file: every read
// returns the payload of some commit, whole.
//
// A lookup can fail for an instant while rename(2) replaces the symlink. On
// macOS 26.5 (Darwin 25.5) a bare reader loop against a symlink swapped as fast
// as possible, with no marvel code in it, failed about 1 read in 100 with
// EINVAL, and about 1 in 100,000 here at twenty swaps. RENAME_SWAP cut the
// bare-loop rate to about 1 in 100,000 and did not remove it. So this test
// holds the content to zero errors and bounds the transient lookup failures
// at 0.1% of reads, and logs the count. Production swaps are minutes apart.
func TestRealGitReaderSeesNoTornFileDuringSwaps(t *testing.T) {
	repo, b := realViewFixture(t)
	// payloads maps each commit id to the payload that commit holds. The
	// writer adds a commit before it refreshes onto it, so a reader that sees
	// a tree finds its commit here.
	var pmu sync.Mutex
	payloads := map[string]string{}
	record := func(n int) {
		sha := commitPayload(t, repo, n)
		pmu.Lock()
		payloads[sha] = fmt.Sprintf("payload-%d", n)
		pmu.Unlock()
	}
	record(0)
	if _, err := b.Refresh(context.Background()); err != nil {
		t.Fatalf("first Refresh: %v", err)
	}

	const swaps = 20
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
	var reads, transient int
	// check reads one file through cur and classifies the outcome.
	check := func(rel string, valid func(string) bool) {
		got, err := os.ReadFile(filepath.Join(b.Dir, "cur", rel))
		switch {
		case err != nil && (errors.Is(err, syscall.EINVAL) || errors.Is(err, fs.ErrNotExist)):
			transient++
		case err != nil:
			note("%s: %v", rel, err)
		case !valid(string(got)):
			note("%s: torn or wrong content %q", rel, got)
		}
		reads++
	}
	isPayload := func(s string) bool {
		var n int
		_, err := fmt.Sscanf(s, "payload-%d", &n)
		return err == nil && n >= 0 && n <= swaps && s == fmt.Sprintf("payload-%d", n)
	}
	isSHA := func(s string) bool {
		pmu.Lock()
		defer pmu.Unlock()
		_, known := payloads[strings.TrimSpace(s)]
		return known
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			check(filepath.Join("a", "b", "payload"), isPayload)
			check("top", isPayload)
			check("VIEW_SHA", isSHA)
		}
	}()

	for n := 1; n <= swaps; n++ {
		record(n)
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
	// Every tree holds its own commit: VIEW_SHA names the directory it sits
	// in, and the payload is that commit's, not the previous one's.
	trees, err := os.ReadDir(filepath.Join(b.Dir, "trees"))
	if err != nil {
		t.Fatal(err)
	}
	if len(trees) != swaps+1 {
		t.Errorf("trees = %d, want %d, one per commit", len(trees), swaps+1)
	}
	for _, e := range trees {
		root := filepath.Join(b.Dir, "trees", e.Name())
		want, known := payloads[e.Name()]
		if !known {
			t.Errorf("tree %s is not a commit the test made", e.Name())
			continue
		}
		if got := strings.TrimSpace(mustRead(t, filepath.Join(root, "VIEW_SHA"))); got != e.Name() {
			t.Errorf("tree %s: VIEW_SHA = %s, want its own commit", e.Name(), got)
		}
		for _, f := range []string{filepath.Join("a", "b", "payload"), "top"} {
			if got := mustRead(t, filepath.Join(root, f)); got != want {
				t.Errorf("tree %s: %s = %q, want %q", e.Name(), f, got, want)
			}
		}
	}
	t.Logf("%d reads, %d transient lookup failures during %d swaps", reads, transient, swaps)
	if float64(transient) > 0.001*float64(reads) {
		t.Errorf("%d of %d reads failed a lookup, want at most 0.1%%", transient, reads)
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
