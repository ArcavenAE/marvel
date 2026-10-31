package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/childproof"
	"github.com/arcavenae/marvel/internal/paths"
)

const sweepHelperEnv = "SWEEP_STANDIN_HELPER"

// TestSweepHelperProcess is the stand-in the sweep tests start. It does
// nothing but wait, and carries the argument "app-server" so it reads like
// the daemon a pid file names. It is a no-op unless a test started it.
func TestSweepHelperProcess(t *testing.T) {
	if os.Getenv(sweepHelperEnv) != "1" {
		t.Skip("helper process, started by the sweep tests")
	}
	time.Sleep(2 * time.Minute)
}

// standIn is a process the test started and owns.
type standIn struct {
	cmd  *exec.Cmd
	done chan struct{}
}

// startStandIn starts a helper whose argv carries arg. The test kills it on
// cleanup, so no stand-in outlives it.
func startStandIn(t *testing.T, arg string) *standIn {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSweepHelperProcess$", arg)
	cmd.Env = append(os.Environ(), sweepHelperEnv+"=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := &standIn{cmd: cmd, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(s.done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-s.done
	})
	return s
}

func (s *standIn) pid() int { return s.cmd.Process.Pid }

func (s *standIn) exited(within time.Duration) bool {
	select {
	case <-s.done:
		return true
	case <-time.After(within):
		return false
	}
}

// startOf is the identity start the kernel reports for the stand-in, as the
// seconds and microseconds codex would have written.
func (s *standIn) startOf(t *testing.T) (sec, usec int64) {
	t.Helper()
	var id childproof.Identity
	var err error
	for range 50 {
		id, err = childproof.ReadIdentityForTest(s.pid())
		if err == nil && len(id.Argv) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("read the stand-in's identity: %v", err)
	}
	rest, ok := strings.CutPrefix(id.Start, "darwin:")
	if !ok {
		t.Skipf("start %q is not comparable on %s", id.Start, runtime.GOOS)
	}
	secStr, usecStr, _ := strings.Cut(rest, ".")
	sec, _ = strconv.ParseInt(secStr, 10, 64)
	usec, _ = strconv.ParseInt(usecStr, 10, 64)
	return sec, usec
}

// writePidFile writes a daemon pid file in the shape codex writes.
func writePidFile(t *testing.T, home, name string, pid int, sec, usec int64) {
	t.Helper()
	dir := filepath.Join(home, "app-server-daemon")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	rec := map[string]any{
		"pid": pid,
		"processIdentity": map[string]any{
			"startSeconds":      sec,
			"startMicroseconds": usec,
		},
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// newBase is a private base directory under the test's scratch space.
func newBase(t *testing.T) string {
	t.Helper()
	base := filepath.Join(t.TempDir(), "h")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	return base
}

func mkHome(t *testing.T, base, name string, history bool) string {
	t.Helper()
	home := filepath.Join(base, name)
	if err := os.MkdirAll(filepath.Join(home, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if history {
		if err := os.WriteFile(filepath.Join(home, "sessions", "rollout.jsonl"), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func sweeperFor(bases []string, recorded ...string) homeSweeper {
	return homeSweeper{bases: bases, recorded: recorded, prober: childproof.New()}
}

func entryFor(t *testing.T, sw HomeSweep, home string) HomeEntry {
	t.Helper()
	for _, h := range sw.Homes {
		if h.Path == home {
			return h
		}
	}
	t.Fatalf("home %s is not in the sweep: %+v", home, sw.Homes)
	return HomeEntry{}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// snapshot lists every path under root with its mode, for a before and after
// comparison.
func snapshot(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s %v %d\n", p, fi.Mode(), fi.Size())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestSweepClassifiesHomesAndIgnoresWhatIsNotAHome(t *testing.T) {
	base := newBase(t)
	live := mkHome(t, base, "aaaaaaaaaaaa", true)
	orphan := mkHome(t, base, "bbbbbbbbbbbb", false)
	emptyHistory := filepath.Join(base, "cccccccccccc")
	if err := os.Mkdir(emptyHistory, 0o700); err != nil { // no sessions directory at all
		t.Fatal(err)
	}
	kept := mkHome(t, base, "dddddddddddd", true)

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "precious"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Things under the base that are not homes: a wrong name, a name of the right
	// shape that is a symlink, and one that is a plain file.
	notHex := mkHome(t, base, "notahomedir", false)
	if err := os.Symlink(outside, filepath.Join(base, "eeeeeeeeeeee")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "ffffffffffff"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	sw := sweeperFor([]string{base}, live).sweep(false)
	want := map[string]string{live: HomeLive, orphan: HomeOrphan, emptyHistory: HomeOrphan, kept: HomeKeptHistory}
	if len(sw.Homes) != len(want) {
		t.Fatalf("listed %d homes, want %d: %+v", len(sw.Homes), len(want), sw.Homes)
	}
	for home, state := range want {
		if got := entryFor(t, sw, home).State; got != state {
			t.Errorf("%s: state %q, want %q", home, got, state)
		}
	}
	for _, h := range sw.Homes {
		if h.Path == notHex || strings.HasSuffix(h.Path, "eeeeeeeeeeee") || strings.HasSuffix(h.Path, "ffffffffffff") {
			t.Errorf("listed something that is not a home: %s", h.Path)
		}
	}
}

func TestSweepDryRunChangesNothing(t *testing.T) {
	base := newBase(t)
	live := mkHome(t, base, "aaaaaaaaaaaa", true)
	mkHome(t, base, "bbbbbbbbbbbb", false)
	mkHome(t, base, "dddddddddddd", true)
	mkHome(t, base, "cccccccccccc", false) // an orphan with no daemon: nothing but confirm keeps it
	sa := startStandIn(t, "app-server")
	sec, usec := sa.startOf(t)
	writePidFile(t, filepath.Join(base, "bbbbbbbbbbbb"), "daemon.pid", sa.pid(), sec, usec)

	before := snapshot(t, filepath.Dir(base))
	sw := sweeperFor([]string{base}, live).sweep(false)
	if sw.Confirmed {
		t.Error("a dry run reports itself confirmed")
	}
	if after := snapshot(t, filepath.Dir(base)); after != before {
		t.Errorf("a dry run changed the tree:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if sa.exited(300 * time.Millisecond) {
		t.Error("a dry run signalled the daemon")
	}
	var d DaemonEntry
	for _, h := range sw.Homes {
		if len(h.Daemons) > 0 {
			d = h.Daemons[0]
		}
	}
	if runtime.GOOS == "darwin" && (d.Pid != sa.pid() || d.Action != DaemonSignal || d.Signalled) {
		t.Errorf("the listing should say the daemon would be signalled, not that it was: %+v", d)
	}
}

func TestSweepConfirmRemovesOnlyOrphansWithNoHistory(t *testing.T) {
	base := newBase(t)
	live := mkHome(t, base, "aaaaaaaaaaaa", true)
	orphan := mkHome(t, base, "bbbbbbbbbbbb", false)
	kept := mkHome(t, base, "dddddddddddd", true)
	notHex := mkHome(t, base, "notahomedir", false)
	outside := t.TempDir()
	precious := filepath.Join(outside, "precious")
	if err := os.WriteFile(precious, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A login link inside the orphan points outside; removing the home removes
	// the link, never what it points at.
	if err := os.Symlink(precious, filepath.Join(orphan, "auth.json")); err != nil {
		t.Fatal(err)
	}

	sw := sweeperFor([]string{base}, live).sweep(true)
	if !sw.Confirmed {
		t.Error("a confirmed sweep does not say so")
	}
	if exists(orphan) {
		t.Error("the orphan home was not removed")
	}
	for _, keep := range []string{live, kept, notHex, precious} {
		if !exists(keep) {
			t.Errorf("%s was removed", keep)
		}
	}
	if !entryFor(t, sw, orphan).Removed {
		t.Error("the listing does not say the orphan was removed")
	}
}

func TestSweepNeverTouchesALiveHomeOrItsDaemon(t *testing.T) {
	base := newBase(t)
	live := mkHome(t, base, "aaaaaaaaaaaa", false) // recorded, and no history
	sa := startStandIn(t, "app-server")
	sec, usec := sa.startOf(t)
	writePidFile(t, live, "daemon.pid", sa.pid(), sec, usec)

	sw := sweeperFor([]string{base}, live).sweep(true)
	if got := entryFor(t, sw, live); got.State != HomeLive || got.Removed || len(got.Daemons) != 0 {
		t.Errorf("live home entry = %+v", got)
	}
	if !exists(live) {
		t.Error("a recorded home was removed")
	}
	if sa.exited(300 * time.Millisecond) {
		t.Error("the daemon of a recorded home was signalled")
	}
}

func TestSweepRecognisesARecordedHomeByWhatItIsNotByHowItIsSpelled(t *testing.T) {
	base := newBase(t)
	home := mkHome(t, base, "aaaaaaaaaaaa", false)
	link := filepath.Join(t.TempDir(), "via-link")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	// The record spells the home through a symlink, the sweep finds it directly.
	sw := sweeperFor([]string{base}, link).sweep(true)
	if got := entryFor(t, sw, home); got.State != HomeLive {
		t.Errorf("a home recorded through a link reads %q, want live", got.State)
	}
	if !exists(home) {
		t.Error("a home recorded through a link was removed")
	}
}

func TestSweepSignalsAProvenDaemonAndKeepsAHomeWithHistory(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("a daemon is proven by a start time that is only comparable on darwin")
	}
	base := newBase(t)
	orphan := mkHome(t, base, "bbbbbbbbbbbb", false)
	kept := mkHome(t, base, "dddddddddddd", true)
	a, b := startStandIn(t, "app-server"), startStandIn(t, "app-server")
	sa, ua := a.startOf(t)
	sb, ub := b.startOf(t)
	writePidFile(t, orphan, "daemon.pid", a.pid(), sa, ua)
	writePidFile(t, kept, "daemon-updater.pid", b.pid(), sb, ub)

	sw := sweeperFor([]string{base}).sweep(true)
	if !a.exited(5*time.Second) || !b.exited(5*time.Second) {
		t.Fatal("a proven daemon was not signalled")
	}
	if exists(orphan) {
		t.Error("the orphan home was kept")
	}
	if !exists(kept) {
		t.Error("the home holding history was removed")
	}
	for _, home := range []string{orphan, kept} {
		d := entryFor(t, sw, home).Daemons
		if len(d) != 1 || !d[0].Signalled {
			t.Errorf("%s: daemons = %+v, want one signalled", home, d)
		}
	}
	if got := entryFor(t, sw, kept); got.State != HomeKeptHistory || got.Removed {
		t.Errorf("kept home entry = %+v", got)
	}
}

func TestSweepLeavesAForeignOrReusedProcessAlone(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("a daemon is proven by a start time that is only comparable on darwin")
	}
	base := newBase(t)
	stale := mkHome(t, base, "aaaaaaaaaaaa", false)
	notDaemon := mkHome(t, base, "bbbbbbbbbbbb", false)
	unrecorded := mkHome(t, base, "cccccccccccc", false)
	garbage := mkHome(t, base, "dddddddddddd", false)

	reused := startStandIn(t, "app-server")
	sec, usec := reused.startOf(t)
	// The pid file says this pid started a second earlier: the pid was reused.
	writePidFile(t, stale, "daemon.pid", reused.pid(), sec-1, usec)

	plain := startStandIn(t, "serve")
	sec, usec = plain.startOf(t)
	writePidFile(t, notDaemon, "daemon.pid", plain.pid(), sec, usec)

	// A process that carries the argument but is written in no pid file.
	foreign := startStandIn(t, "app-server")

	// A pid file that is not codex's, and one naming pid 1.
	if err := os.MkdirAll(filepath.Join(garbage, "app-server-daemon"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(garbage, "app-server-daemon", "daemon.pid"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	writePidFile(t, unrecorded, "daemon.pid", 1, 1, 1)

	sw := sweeperFor([]string{base}).sweep(true)
	for name, p := range map[string]*standIn{"reused pid": reused, "not an app-server": plain, "unrecorded process": foreign} {
		if p.exited(500 * time.Millisecond) {
			t.Errorf("%s: the process was signalled", name)
		}
	}
	for _, home := range []string{stale, notDaemon, unrecorded, garbage} {
		for _, d := range entryFor(t, sw, home).Daemons {
			if d.Action != DaemonLeft || d.Signalled {
				t.Errorf("%s: daemon entry %+v, want left alone", home, d)
			}
		}
	}
	// A pid that now belongs to another process is accounted for, so its home goes;
	// a pid file that cannot be read or names pid 1 is not, so its home stays.
	for _, gone := range []string{stale, notDaemon} {
		if exists(gone) {
			t.Errorf("%s: kept although its pid belongs to another process", gone)
		}
	}
	for _, kept := range []string{unrecorded, garbage} {
		if !exists(kept) {
			t.Errorf("%s: removed although its daemon is not accounted for", kept)
		}
	}
}

func TestSweepKeepsAnOrphanWhoseDaemonStateIsUnknown(t *testing.T) {
	base := newBase(t)
	badJSON := mkHome(t, base, "aaaaaaaaaaaa", false)
	noStart := mkHome(t, base, "bbbbbbbbbbbb", false)
	notRegular := mkHome(t, base, "cccccccccccc", false)
	if err := os.MkdirAll(filepath.Join(badJSON, "app-server-daemon"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badJSON, "app-server-daemon", "daemon.pid"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A start time of zero cannot be compared on any platform, so the daemon cannot
	// be proven or ruled out: the stand-in of every non-darwin host.
	writePidFile(t, noStart, "daemon.pid", os.Getpid(), 0, 0)
	if err := os.MkdirAll(filepath.Join(notRegular, "app-server-daemon", "daemon.pid"), 0o700); err != nil {
		t.Fatal(err)
	}

	sw := sweeperFor([]string{base}).sweep(true)
	for _, home := range []string{badJSON, noStart, notRegular} {
		got := entryFor(t, sw, home)
		if !exists(home) || got.Removed {
			t.Errorf("%s was removed while its daemon is unaccounted for: %+v", home, got)
		}
		if !strings.Contains(got.Why, "not accounted for") {
			t.Errorf("%s: why = %q, want the reason it was kept", home, got.Why)
		}
	}
}

func TestSweepRemovesAnOrphanWhoseDaemonIsGone(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("a daemon is proven by a start time that is only comparable on darwin")
	}
	base := newBase(t)
	orphan := mkHome(t, base, "aaaaaaaaaaaa", false)
	d := startStandIn(t, "app-server")
	sec, usec := d.startOf(t)
	writePidFile(t, orphan, "daemon.pid", d.pid(), sec, usec)
	if err := d.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if !d.exited(5 * time.Second) {
		t.Fatal("the stand-in did not exit")
	}
	sw := sweeperFor([]string{base}).sweep(true)
	if got := entryFor(t, sw, orphan); !got.Removed || exists(orphan) {
		t.Errorf("a home whose daemon is gone was kept: %+v", got)
	}
}

func TestSweepReReadsRecordsAndHistoryJustBeforeRemoving(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(home string) []string // runs at the second read of the records
		want   string
	}{
		"a record names the home since the listing": {
			change: func(home string) []string { return []string{home} },
			want:   "changed during the sweep",
		},
		"history appeared since the listing": {
			change: func(home string) []string {
				_ = os.WriteFile(filepath.Join(home, "sessions", "rollout.jsonl"), []byte("{}\n"), 0o600)
				return nil
			},
			want: "changed during the sweep",
		},
	} {
		t.Run(name, func(t *testing.T) {
			base := newBase(t)
			home := mkHome(t, base, "aaaaaaaaaaaa", false)
			reads := 0
			s := sweeperFor([]string{base})
			s.recordedNow = func() []string {
				reads++
				if reads == 1 {
					return nil // the listing sees no record
				}
				return tc.change(home)
			}
			sw := s.sweep(true)
			got := entryFor(t, sw, home)
			if !exists(home) || got.Removed || !strings.Contains(got.Why, tc.want) {
				t.Errorf("home removed or not explained after a change during the sweep: %+v", got)
			}
		})
	}
}

func TestSweepAndSpawnShareOneTempBase(t *testing.T) {
	// TMPDIR on macOS is a long per-user path; the spawn path uses the literal /tmp.
	t.Setenv("TMPDIR", t.TempDir())
	mgr := &Manager{store: api.NewStore(), HarnessHomeDir: newBase(t)}
	s := mgr.homeSweeper()
	if s.tmpDir != "/tmp" {
		t.Errorf("the sweep counts other layouts under %q, want /tmp", s.tmpDir)
	}
	layout, err := paths.Default()
	if err != nil {
		t.Skipf("no layout: %v", err)
	}
	want := legacyHarnessHomeBase(layout.Tag())
	if !strings.HasPrefix(want, "/tmp/marvel-h-") {
		t.Errorf("legacy base = %q, want one under /tmp/marvel-h-", want)
	}
	if !slices.Contains(s.bases, want) {
		t.Errorf("the sweep's bases %v lack the spawn path's temp base %s", s.bases, want)
	}
	for _, b := range s.bases {
		if strings.HasPrefix(b, os.Getenv("TMPDIR")) && b != mgr.HarnessHomeDir && !strings.HasPrefix(b, mgr.HarnessHomeDir) {
			t.Errorf("the sweep looks under TMPDIR: %s", b)
		}
	}
}

// TestDefaultHarnessHomeDirUsesTheSharedTempBase fails if the spawn path goes
// back to spelling its own temp base, so it cannot drift from the sweep's.
func TestDefaultHarnessHomeDirUsesTheSharedTempBase(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "manager.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "harnessHomeDirs" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "legacyHarnessHomeBase" {
					found = true
				}
			}
			return true
		})
	}
	if !found {
		t.Error("harnessHomeDirs (the body of defaultHarnessHomeDir) does not call legacyHarnessHomeBase; the spawn path and the sweep could drift")
	}
}

func TestSweepKeepsAnOrphanWhoseDaemonCannotBeSignalled(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("a daemon is proven by a start time that is only comparable on darwin")
	}
	base := newBase(t)
	orphan := mkHome(t, base, "bbbbbbbbbbbb", false)
	a := startStandIn(t, "app-server")
	sec, usec := a.startOf(t)
	writePidFile(t, orphan, "daemon.pid", a.pid(), sec, usec)

	s := sweeperFor([]string{base})
	// A prober whose kill always fails stands in for a daemon that cannot be signalled.
	p, err := childproof.NewForTest(childproof.ReadIdentityForTest, func(int, syscall.Signal) error { return syscall.EPERM })
	if err != nil {
		t.Fatal(err)
	}
	s.prober = p
	sw := s.sweep(true)
	if !exists(orphan) {
		t.Error("an orphan home was removed while its daemon was still running")
	}
	if got := entryFor(t, sw, orphan); got.Removed || !strings.Contains(got.Why, "not accounted for") || !strings.Contains(got.Why, "signal failed") {
		t.Errorf("entry = %+v", got)
	}
}

func TestSweepSkipsABaseThatIsNotPrivateToThisUser(t *testing.T) {
	loose := filepath.Join(t.TempDir(), "h")
	if err := os.Mkdir(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	home := mkHome(t, loose, "bbbbbbbbbbbb", false)
	target := newBase(t)
	linked := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(target, linked); err != nil {
		t.Fatal(err)
	}
	mkHome(t, target, "cccccccccccc", false)

	sw := sweeperFor([]string{loose, linked, filepath.Join(t.TempDir(), "absent")}).sweep(true)
	if len(sw.Homes) != 0 || len(sw.Bases) != 0 {
		t.Errorf("swept a base it should have skipped: %+v", sw)
	}
	if len(sw.Notes) != 2 {
		t.Errorf("notes = %v, want one each for the loose and the linked base and none for the absent one", sw.Notes)
	}
	if !exists(home) || !exists(filepath.Join(target, "cccccccccccc")) {
		t.Error("a home under a skipped base was removed")
	}
}

func TestSweepCountsOtherLayoutsAndTouchesNothingThere(t *testing.T) {
	tmp := t.TempDir()
	own := filepath.Join(tmp, "marvel-h-11111111")
	other := filepath.Join(tmp, "marvel-h-22222222")
	empty := filepath.Join(tmp, "marvel-h-33333333")
	notBase := filepath.Join(tmp, "marvel-h-zz")
	for _, d := range []string{own, other, empty, notBase} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	mkHome(t, other, "aaaaaaaaaaaa", false)
	mkHome(t, other, "bbbbbbbbbbbb", true)
	mkHome(t, own, "cccccccccccc", false)

	s := sweeperFor([]string{own})
	s.tmpDir, s.ownTmpBase = tmp, "marvel-h-11111111"
	before := snapshot(t, other) + snapshot(t, empty)
	sw := s.sweep(true)
	if sw.OtherBases != 2 || sw.OtherHomes != 2 {
		t.Errorf("other layouts = %d bases, %d homes, want 2 and 2", sw.OtherBases, sw.OtherHomes)
	}
	if after := snapshot(t, other) + snapshot(t, empty); after != before {
		t.Error("a confirmed sweep changed another layout's base")
	}
	if exists(filepath.Join(own, "cccccccccccc")) {
		t.Error("this layout's own orphan was not removed")
	}
}

func TestSweepHomesNamesEverySessionRecordAsLive(t *testing.T) {
	base := newBase(t)
	recordedHome := mkHome(t, base, "aaaaaaaaaaaa", false)
	orphan := mkHome(t, base, "bbbbbbbbbbbb", false)
	mgr := homeManager(t)
	mgr.HarnessHomeDir = base
	sess := sessionFor("coder", "codex")
	sess.HarnessHome = recordedHome
	if err := mgr.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}

	sw := mgr.SweepHomes(false)
	if got := entryFor(t, sw, recordedHome).State; got != HomeLive {
		t.Errorf("the recorded home reads %q, want live", got)
	}
	if got := entryFor(t, sw, orphan).State; got != HomeOrphan {
		t.Errorf("the unrecorded home reads %q, want orphan", got)
	}
}

// reapFixture is a manager whose base holds one seat's home and that seat's
// session record, so a delete can be followed by what happens to the home.
func reapFixture(t *testing.T, history bool) (mgr *Manager, key, home string) {
	t.Helper()
	base := newBase(t)
	home = mkHome(t, base, "aaaaaaaaaaaa", history)
	mgr = homeManager(t)
	mgr.HarnessHomeDir = base
	sess := sessionFor("coder", "codex")
	sess.HarnessHome = home
	if err := mgr.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	return mgr, sess.Key(), home
}

func TestDeleteAndReapRemovesTheHomeAndSignalsItsDaemon(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("a daemon is proven by a start time that is only comparable on darwin")
	}
	mgr, key, home := reapFixture(t, false)
	d := startStandIn(t, "app-server")
	sec, usec := d.startOf(t)
	writePidFile(t, home, "daemon.pid", d.pid(), sec, usec)
	foreign := startStandIn(t, "app-server") // in no pid file

	if err := mgr.DeleteAndReap(key); err != nil {
		t.Fatal(err)
	}
	if exists(home) {
		t.Error("the stopped seat's home was kept")
	}
	if !d.exited(5 * time.Second) {
		t.Error("the stopped seat's daemon was not signalled")
	}
	if foreign.exited(500 * time.Millisecond) {
		t.Error("a process in no pid file was signalled")
	}
	if _, err := mgr.store.GetSession(key); err == nil {
		t.Error("the session record is still there")
	}
}

func TestDeleteAndReapKeepsAHomeThatHoldsHistory(t *testing.T) {
	mgr, key, home := reapFixture(t, true)
	if err := mgr.DeleteAndReap(key); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(home, "sessions", "rollout.jsonl")) {
		t.Error("codex history was removed with the seat")
	}
}

func TestPlainDeleteLeavesTheHomeAlone(t *testing.T) {
	// Delete is also the restart, shift and respawn path; the same key reuses the
	// same home, so it must never reap.
	mgr, key, home := reapFixture(t, false)
	if err := mgr.Delete(key); err != nil {
		t.Fatal(err)
	}
	if !exists(home) {
		t.Error("Delete removed the home; a restart would lose its login")
	}
}

func TestReapKeepsAHomeTheKeyWasGivenAgain(t *testing.T) {
	mgr, key, home := reapFixture(t, false)
	if err := mgr.Delete(key); err != nil {
		t.Fatal(err)
	}
	// The reconciler gives the same key a new session, which records the same home,
	// before the reap reads the records.
	again := sessionFor("coder", "codex")
	again.HarnessHome = home
	if err := mgr.store.CreateSession(again); err != nil {
		t.Fatal(err)
	}
	mgr.reapSeatHome(key, home)
	if !exists(home) {
		t.Error("a home a new session record names was removed")
	}
}

func TestReapLeavesWhatIsNotAMarvelHome(t *testing.T) {
	mgr, _, _ := reapFixture(t, false)
	elsewhere := mkHome(t, t.TempDir(), "bbbbbbbbbbbb", false) // right name, not under a base
	badName := mkHome(t, mgr.HarnessHomeDir, "notahomedir", false)
	for _, home := range []string{elsewhere, badName, "", filepath.Dir(mgr.HarnessHomeDir)} {
		mgr.reapSeatHome("k", home)
	}
	for _, home := range []string{elsewhere, badName, filepath.Dir(mgr.HarnessHomeDir)} {
		if !exists(home) {
			t.Errorf("%s was removed", home)
		}
	}
}

func TestDeleteAndReapOfAMissingSessionTouchesNothing(t *testing.T) {
	mgr, _, home := reapFixture(t, false)
	if err := mgr.DeleteAndReap("no/such/session"); err == nil {
		t.Error("deleting a session that does not exist reported success")
	}
	if !exists(home) {
		t.Error("a home was removed for a session that was never deleted")
	}
}

// TestSweepDoesNotCallAPidGoneUntilItIsGone covers the cross-check in
// unprovenWhy: a read error without a start time reads as "dead" to the proof,
// but a process that exists whose identity cannot be read (another user's, or
// not yet settled) may still be the daemon, so its home stays. Only a pid the
// kernel says does not exist lets the home go.
func TestSweepDoesNotCallAPidGoneUntilItIsGone(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("a recorded start time is only comparable on darwin")
	}
	for name, tc := range map[string]struct {
		probe       error // what kill(pid, 0) answers
		wantRemoved bool
	}{
		"the pid still exists but cannot be read": {probe: nil, wantRemoved: false},
		"the pid does not exist":                  {probe: syscall.ESRCH, wantRemoved: true},
	} {
		t.Run(name, func(t *testing.T) {
			base := newBase(t)
			orphan := mkHome(t, base, "aaaaaaaaaaaa", false)
			writePidFile(t, orphan, "daemon.pid", 4242, 1000, 5)
			read := func(int) (childproof.Identity, error) { return childproof.Identity{}, syscall.EPERM }
			kill := func(_ int, sig syscall.Signal) error {
				if sig != 0 {
					t.Errorf("signal %d was sent", sig)
					return nil
				}
				return tc.probe
			}
			p, err := childproof.NewForTest(read, kill)
			if err != nil {
				t.Fatal(err)
			}
			s := sweeperFor([]string{base})
			s.prober = p
			got := entryFor(t, s.sweep(true), orphan)
			if got.Removed != tc.wantRemoved || exists(orphan) == tc.wantRemoved {
				t.Errorf("removed=%v exists=%v, want removed=%v: %+v", got.Removed, exists(orphan), tc.wantRemoved, got)
			}
			if !tc.wantRemoved && !strings.Contains(got.Why, "identity could not be read") {
				t.Errorf("why = %q, want the reason the home was kept", got.Why)
			}
		})
	}
}

// notMine answers "not owned by this user" for every path it is given a name
// for, and "owned" for the rest: a directory owned by another user cannot be
// made without privilege, so the check's answer is what the tests control.
func notMine(names ...string) func(os.FileInfo) bool {
	return func(fi os.FileInfo) bool { return !slices.Contains(names, fi.Name()) }
}

func TestSweepLeavesAHomeThatAnotherUserOwns(t *testing.T) {
	base := newBase(t)
	theirs := mkHome(t, base, "aaaaaaaaaaaa", false)
	mine := mkHome(t, base, "bbbbbbbbbbbb", false)
	s := sweeperFor([]string{base})
	s.owned = notMine("aaaaaaaaaaaa")
	sw := s.sweep(true)
	if !exists(theirs) {
		t.Error("a home the ownership check refused was removed")
	}
	for _, h := range sw.Homes {
		if h.Path == theirs {
			t.Errorf("a home the ownership check refused was listed: %+v", h)
		}
	}
	if exists(mine) {
		t.Error("the owned orphan was not removed")
	}
}

func TestSweepSkipsABaseThatAnotherUserOwns(t *testing.T) {
	base := newBase(t)
	home := mkHome(t, base, "aaaaaaaaaaaa", false)
	s := sweeperFor([]string{base})
	s.owned = notMine("h") // newBase names the base "h"
	sw := s.sweep(true)
	if !exists(home) || len(sw.Homes) != 0 || len(sw.Bases) != 0 {
		t.Errorf("swept a base the ownership check refused: %+v", sw)
	}
	if len(sw.Notes) != 1 || !strings.Contains(sw.Notes[0], "not owned by this user") {
		t.Errorf("notes = %v, want the ownership refusal", sw.Notes)
	}
}

func TestReapLeavesAHomeThatAnotherUserOwns(t *testing.T) {
	mgr, key, home := reapFixture(t, false)
	if err := mgr.Delete(key); err != nil {
		t.Fatal(err)
	}
	mgr.ownerCheck = notMine("aaaaaaaaaaaa")
	mgr.reapSeatHome(key, home)
	if !exists(home) {
		t.Error("a home the ownership check refused was reaped")
	}
}

func TestDeleteAndReapReapsNothingWhenThePaneCannotBeKilled(t *testing.T) {
	mgr, key, home := reapFixture(t, false)
	mgr.killPane = func(string) error { return errors.New("kill-pane: server busy") }
	if err := mgr.store.UpdateSession(key, func(s *api.Session) error {
		s.PaneID = "%9"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	if err := mgr.DeleteAndReap(key); err == nil {
		t.Fatal("a delete whose kill failed reported success")
	}
	if !exists(home) {
		t.Error("the home of a seat that could not be stopped was removed")
	}
	// The kept row names the home, so the records alone would also keep it; the
	// reap itself must not even run, which its log line shows.
	if strings.Contains(logged.String(), "harness home") {
		t.Errorf("the reap ran after a failed delete: %s", logged.String())
	}
}
