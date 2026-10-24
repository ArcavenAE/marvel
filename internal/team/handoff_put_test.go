package team

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// Tests for handoff.put (docs/design/read-only-seat-handoff.md, section 2):
// the daemon writes the declared handoff file for a seat that cannot, behind a
// gate of four conditions. The controller is built without a session manager,
// so none of this needs tmux.

const putMarker = "END HANDOFF"

// realTempDir is t.TempDir with its links resolved. The gate refuses a
// directory with a link in its path, and on macOS the temp root sits under
// /var, which is one.
func realTempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

type putFixture struct {
	t     *testing.T
	store *api.Store
	ctrl  *Controller
	ring  *events.Ring
	dir   string
	sess  api.Session
	token string
	team  string
}

// newPutFixture builds one team with one max-age role that declares a handoff
// file, a bound running session, and (when asked) the pending request the
// controller records through requestHandoff, as it does for a real seat.
func newPutFixture(t *testing.T, request bool) *putFixture {
	t.Helper()
	dir := realTempDir(t)
	store := api.NewStore()
	ctrl := NewController(store, nil)
	ring := events.NewRing(0)
	ctrl.Events = ring
	ctrl.Notify = func(api.Session, string, string) error { return nil }
	clock := newTestClock(listEpoch)
	ctrl.now = clock.Now

	role := maxAgeRole(dir)
	createTeamFixture(t, store, "ws", "squad", []api.Role{role})

	tok, hash, err := api.NewHeartbeatToken()
	if err != nil {
		t.Fatal(err)
	}
	sess := api.Session{
		Name: "squad-" + testShiftRole + "-g1-0", Workspace: "ws", Team: "squad",
		Role: testShiftRole, Generation: 1, State: api.SessionRunning,
		CreatedAt:      listEpoch.Add(-10 * time.Hour),
		HeartbeatToken: tok, HeartbeatTokenHash: hash,
	}
	seedSession(t, store, sess)

	f := &putFixture{t: t, store: store, ctrl: ctrl, ring: ring, dir: dir, sess: sess, token: tok, team: "ws/squad"}
	if request {
		f.request()
	}
	return f
}

func (f *putFixture) request() {
	f.t.Helper()
	team, err := f.store.GetTeam(f.team)
	if err != nil {
		f.t.Fatal(err)
	}
	cond := team.Roles[0].Shift.Any[0]
	f.ctrl.requestHandoff(&team, &team.Roles[0], cond, f.sess, 10*time.Hour, ageStageQuiet, listEpoch)
}

func (f *putFixture) path() string { return filepath.Join(f.dir, f.sess.Name+".md") }

func (f *putFixture) req() api.ShiftRequest {
	f.t.Helper()
	team, err := f.store.GetTeam(f.team)
	if err != nil {
		f.t.Fatal(err)
	}
	return team.ShiftRequests[testShiftRole]
}

func (f *putFixture) count(kind events.Kind) int {
	return len(f.ring.Snapshot(events.Filter{Kind: kind}, 0))
}

// entries lists the handoff directory, so a test can see that a refusal left
// nothing behind, a temporary file included.
func entries(t *testing.T, dir string) []string {
	t.Helper()
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, de := range des {
		out = append(out, de.Name())
	}
	return out
}

func TestRequestHandoffRecordsTheDirectoryIdentity(t *testing.T) {
	f := newPutFixture(t, true)
	var st syscall.Stat_t
	if err := syscall.Stat(f.dir, &st); err != nil {
		t.Fatal(err)
	}
	req := f.req()
	if req.DirIno == 0 || req.DirIno != st.Ino || req.DirDev != uint64(st.Dev) { //nolint:unconvert // Dev is int32 on darwin
		t.Fatalf("request identity = (dev %d, ino %d), want (dev %d, ino %d)", req.DirDev, req.DirIno, st.Dev, st.Ino)
	}
}

func TestPutHandoffWritesTheDeclaredFileEndingInTheMarker(t *testing.T) {
	f := newPutFixture(t, true)
	got, err := f.ctrl.PutHandoff(f.token, "what I was doing\nwhat is left\n")
	if err != nil {
		t.Fatalf("PutHandoff: %v", err)
	}
	want := "what I was doing\nwhat is left\n" + putMarker + "\n"
	b, err := os.ReadFile(f.path())
	if err != nil || string(b) != want {
		t.Fatalf("file = %q (%v), want %q", b, err, want)
	}
	if got.Path != f.path() || got.Bytes != len(want) {
		t.Fatalf("result = %+v, want path %s and %d bytes", got, f.path(), len(want))
	}
	if !handoffComplete(f.path(), putMarker) {
		t.Fatal("the file marvel wrote is not read as complete by the marker check the shift path uses")
	}
	info, _ := os.Lstat(f.path())
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
	if f.req().PutAt.IsZero() || f.count(events.KindHandoffPut) != 1 {
		t.Fatalf("receipt: PutAt %v, put events %d, want the daemon's receipt recorded once", f.req().PutAt, f.count(events.KindHandoffPut))
	}
	if left := entries(t, f.dir); len(left) != 1 {
		t.Fatalf("directory = %v, want only the handoff file", left)
	}
}

func TestPutHandoffDoesNotDoubleTheMarker(t *testing.T) {
	f := newPutFixture(t, true)
	if _, err := f.ctrl.PutHandoff(f.token, "notes\n"+putMarker+"\n\n"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(f.path())
	if string(b) != "notes\n"+putMarker+"\n" {
		t.Fatalf("file = %q, want the marker once, last", b)
	}
}

func TestPutHandoffReplacesAnExistingRegularFile(t *testing.T) {
	f := newPutFixture(t, true)
	if err := os.WriteFile(f.path(), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ctrl.PutHandoff(f.token, "fresh"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(f.path())
	if string(b) != "fresh\n"+putMarker+"\n" {
		t.Fatalf("file = %q, want the fresh handoff", b)
	}
}

func TestPutHandoffAcceptsExactlyTheCap(t *testing.T) {
	f := newPutFixture(t, true)
	if _, err := f.ctrl.PutHandoff(f.token, strings.Repeat("x", MaxHandoffPutBytes)); err != nil {
		t.Fatalf("a put of exactly the cap: %v", err)
	}
}

// Each row is one way the gate says no. Every refusal must leave no file and
// no temporary file, and be named by an event.
func TestPutHandoffRefusals(t *testing.T) {
	type row struct {
		name  string
		setup func(f *putFixture) (token, text string)
		want  error
	}
	rows := []row{
		{"no token", func(f *putFixture) (string, string) { return "", "x" }, ErrHandoffUnauthorized},
		{"wrong token", func(f *putFixture) (string, string) { return "not-the-token", "x" }, ErrHandoffUnauthorized},
		{"empty text", func(f *putFixture) (string, string) { return f.token, "" }, ErrHandoffTooLarge},
		{"over the cap", func(f *putFixture) (string, string) {
			return f.token, strings.Repeat("x", MaxHandoffPutBytes+1)
		}, ErrHandoffTooLarge},
		{"a sibling replica that holds no request", func(f *putFixture) (string, string) {
			tok, hash, _ := api.NewHeartbeatToken()
			sib := api.Session{
				Name: "squad-" + testShiftRole + "-g1-1", Workspace: "ws", Team: "squad",
				Role: testShiftRole, Generation: 1, State: api.SessionRunning,
				CreatedAt: listEpoch.Add(-10 * time.Hour), HeartbeatToken: tok, HeartbeatTokenHash: hash,
			}
			seedSession(f.t, f.store, sib)
			return tok, "x"
		}, ErrHandoffNotRequested},
		{"an escalated request", func(f *putFixture) (string, string) {
			if err := f.store.UpdateTeam(f.team, func(live *api.Team) error {
				r := live.ShiftRequests[testShiftRole]
				r.Escalated = true
				live.ShiftRequests[testShiftRole] = r
				return nil
			}); err != nil {
				f.t.Fatal(err)
			}
			return f.token, "x"
		}, ErrHandoffNotRequested},
		{"a request recorded with no directory identity", func(f *putFixture) (string, string) {
			if err := f.store.UpdateTeam(f.team, func(live *api.Team) error {
				r := live.ShiftRequests[testShiftRole]
				r.DirDev, r.DirIno = 0, 0
				live.ShiftRequests[testShiftRole] = r
				return nil
			}); err != nil {
				f.t.Fatal(err)
			}
			return f.token, "x"
		}, ErrHandoffDir},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			f := newPutFixture(t, true)
			token, text := r.setup(f)
			_, err := f.ctrl.PutHandoff(token, text)
			if !errors.Is(err, r.want) {
				t.Fatalf("err = %v, want %v", err, r.want)
			}
			if left := entries(t, f.dir); len(left) != 0 {
				t.Fatalf("a refusal left %v behind", left)
			}
			if f.count(events.KindHandoffPutRefused) != 1 || f.count(events.KindHandoffPut) != 0 {
				t.Fatalf("events: refused %d, put %d, want 1 and 0", f.count(events.KindHandoffPutRefused), f.count(events.KindHandoffPut))
			}
			if !f.req().PutAt.IsZero() {
				t.Fatal("a refusal recorded a receipt")
			}
		})
	}
}

func TestPutHandoffRefusesWithNoPendingRequest(t *testing.T) {
	f := newPutFixture(t, false)
	if _, err := f.ctrl.PutHandoff(f.token, "x"); !errors.Is(err, ErrHandoffNotRequested) {
		t.Fatalf("err = %v, want ErrHandoffNotRequested", err)
	}
	if left := entries(t, f.dir); len(left) != 0 {
		t.Fatalf("directory = %v, want empty", left)
	}
}

func TestPutHandoffRefusesARoleWithNoDeclaredFile(t *testing.T) {
	f := newPutFixture(t, true)
	if err := f.store.UpdateTeam(f.team, func(live *api.Team) error {
		live.Roles[0].Shift.Handoff, live.Roles[0].Shift.HandoffMarker = "", ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ctrl.PutHandoff(f.token, "x"); !errors.Is(err, ErrHandoffNoPath) {
		t.Fatalf("err = %v, want ErrHandoffNoPath", err)
	}
}

// The path comes from the session record and the role, never from the
// request: the wire type has no path field, so a put cannot name one. This
// pins that the file lands at the declared path and only there.
func TestPutHandoffWritesOnlyAtTheDeclaredPath(t *testing.T) {
	f := newPutFixture(t, true)
	if _, err := f.ctrl.PutHandoff(f.token, "x"); err != nil {
		t.Fatal(err)
	}
	if got := entries(t, f.dir); len(got) != 1 || got[0] != f.sess.Name+".md" {
		t.Fatalf("directory = %v, want only %s.md", got, f.sess.Name)
	}
}

// A same-uid process can swap the directory after the request. The gate holds
// the recorded identity and checks it on the opened directory, so the swap is
// refused instead of followed.
func TestPutHandoffRefusesADirectoryReplacedSinceTheRequest(t *testing.T) {
	f := newPutFixture(t, true)
	moved := f.dir + ".moved"
	if err := os.Rename(f.dir, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ctrl.PutHandoff(f.token, "x"); !errors.Is(err, ErrHandoffDir) {
		t.Fatalf("err = %v, want ErrHandoffDir", err)
	}
	if left := entries(t, f.dir); len(left) != 0 {
		t.Fatalf("the replacement directory holds %v, want nothing written", left)
	}
	if left := entries(t, moved); len(left) != 0 {
		t.Fatalf("the original directory holds %v, want nothing written", left)
	}
}

func TestPutHandoffRefusesADirectoryThatBecameALink(t *testing.T) {
	f := newPutFixture(t, true)
	elsewhere := realTempDir(t)
	if err := os.Rename(f.dir, f.dir+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, f.dir); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ctrl.PutHandoff(f.token, "x"); !errors.Is(err, ErrHandoffDir) {
		t.Fatalf("err = %v, want ErrHandoffDir", err)
	}
	if left := entries(t, elsewhere); len(left) != 0 {
		t.Fatalf("the link target holds %v, want nothing written through the link", left)
	}
}

// An ancestor that is a link is refused too, which is why the check is on the
// resolved path and not only on the last element.
func TestPutHandoffRefusesAnAncestorThatIsALink(t *testing.T) {
	base := realTempDir(t)
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "handoffs"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	f := newPutFixture(t, false)
	f.dir = filepath.Join(link, "handoffs")
	if err := f.store.UpdateTeam(f.team, func(live *api.Team) error {
		live.Roles[0].Shift.Handoff = filepath.Join(f.dir, "{session}.md")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.request()
	if _, err := f.ctrl.PutHandoff(f.token, "x"); !errors.Is(err, ErrHandoffDir) {
		t.Fatalf("err = %v, want ErrHandoffDir", err)
	}
	if left := entries(t, filepath.Join(real, "handoffs")); len(left) != 0 {
		t.Fatalf("the real directory holds %v, want nothing written through the ancestor link", left)
	}
}

func TestPutHandoffRefusesATargetThatIsALink(t *testing.T) {
	f := newPutFixture(t, true)
	victim := filepath.Join(realTempDir(t), "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, f.path()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ctrl.PutHandoff(f.token, "x"); !errors.Is(err, ErrHandoffDir) {
		t.Fatalf("err = %v, want ErrHandoffDir", err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Fatalf("the link's target was written through: %q", b)
	}
	for _, n := range entries(t, f.dir) {
		if strings.HasPrefix(n, ".") {
			t.Fatalf("a temporary file %s was left behind", n)
		}
	}
}

func TestPutHandoffRefusesATargetThatIsADirectory(t *testing.T) {
	f := newPutFixture(t, true)
	if err := os.Mkdir(f.path(), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ctrl.PutHandoff(f.token, "x"); !errors.Is(err, ErrHandoffDir) {
		t.Fatalf("err = %v, want ErrHandoffDir", err)
	}
}
