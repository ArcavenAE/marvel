package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/tmux"
)

// The launch half of docs/design/workspace-sandbox-shape.md, sections 5 and 7: a
// seat whose placement resolves to container runs its prepared command under the
// container runtime, with the role's container_image. A role with no image is
// refused, never given a default one, and nothing in these paths starts a
// container-placed seat on the host. What happens when no runtime is installed is
// not decided here (card sandbox-refusal-vs-d2); the tests only pin that such a
// seat does not start.

type containerRig struct {
	store *api.Store
	mgr   *Manager
	ring  *events.Ring
	ws    string
	log   string // one argument per line, every call to the fake runtime
	out   string // the inner command writes here
	work  string
	cmd   string // the seat's own command
}

// fakeContainerCLI stands in for docker: it logs each call's arguments, one per
// line with a header, then for `run` executes the last argument with sh, which
// is the prepared command, as a container would.
func fakeContainerCLI(t *testing.T) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "docker")
	log = filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\n{ echo \"--- $1\"; for a; do echo \"$a\"; done; } >> '" + log + "'\n" +
		"if [ \"$1\" = run ]; then\n  for a; do last=$a; done\n  exec sh -c \"$last\"\nfi\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func newContainerRig(t *testing.T, ws string, placement api.Placement, role api.Role) *containerRig {
	t.Helper()
	skipIfNoTmux(t)
	store := api.NewStore()
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(store, driver)
	mgr.SocketPath = filepath.Join(t.TempDir(), "marvel.sock")
	ring := events.NewRing(64)
	mgr.Events = ring
	bin, log := fakeContainerCLI(t)
	mgr.LookPath = func(name string) (string, error) {
		if name == "docker" {
			return bin, nil
		}
		return "", errors.New("not found")
	}
	work, _ := filepath.EvalSymlinks(t.TempDir())
	out, _ := filepath.EvalSymlinks(t.TempDir())
	if err := store.CreateWorkspace(&api.Workspace{Name: ws, Placement: placement, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	cmd := writeInner(t, out)
	role.Runtime.Command = cmd
	if err := store.CreateTeam(&api.Team{
		Name: "squad", Workspace: ws, Generation: 1, Roles: []api.Role{role},
		ConvergencePosture: api.PostureConverge, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.CleanupWorkspace(ws) })
	return &containerRig{store: store, mgr: mgr, ring: ring, ws: ws, log: log, out: out, work: work, cmd: cmd}
}

// writeInner writes the seat's command: it records that it ran, where, and with
// which session name, then sleeps.
func writeInner(t *testing.T, out string) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "inner.sh")
	body := "#!/bin/sh\npwd -P > " + out + "/ran\necho \"$MARVEL_SESSION\" > " + out + "/session\nexec sleep 300\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return script
}

func (r *containerRig) session(runtimeName string) *api.Session {
	return &api.Session{
		Name: "seat-g1-0", Workspace: r.ws, Team: "squad", Role: "seat", Generation: 1,
		WorkDir: r.work, Runtime: api.Runtime{Name: runtimeName, Command: r.cmd},
	}
}

func (r *containerRig) ran(t *testing.T) bool {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(r.out, "ran")); err == nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func (r *containerRig) neverRan(t *testing.T) bool {
	t.Helper()
	time.Sleep(1500 * time.Millisecond)
	_, err := os.Stat(filepath.Join(r.out, "ran"))
	return err != nil
}

func (r *containerRig) calls() []string {
	raw, err := os.ReadFile(r.log)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

func (r *containerRig) refusals() []events.Event {
	return r.ring.Snapshot(events.Filter{Kind: events.KindSessionContainerRefused}, 0)
}

// A container seat with no image is refused: an error that wraps
// ErrPlacementRefused, no session record, nothing started, no call to the
// runtime, and a refusal event of its own kind that names the role and the key.
// It is reported once per change, and the standing record says why.
func TestContainerPlacementWithNoImageIsRefused(t *testing.T) {
	r := newContainerRig(t, "test-ctr-noimg", api.PlacementContainer, api.Role{Name: "seat", Replicas: 1})
	sess := r.session("sleep")
	err := r.mgr.Create(sess)
	if err == nil || !errors.Is(err, ErrPlacementRefused) {
		t.Fatalf("Create = %v, want an error wrapping ErrPlacementRefused", err)
	}
	if !strings.Contains(err.Error(), "container_image") {
		t.Errorf("the error does not name the missing key: %v", err)
	}
	if _, gerr := r.store.GetSession(sess.Key()); gerr == nil {
		t.Error("a refused spawn left a session record")
	}
	if !r.neverRan(t) {
		t.Error("the seat started on the host")
	}
	if len(r.calls()) != 0 {
		t.Errorf("the container runtime was called: %v", r.calls())
	}
	// The reconcile loop retries every tick: a second try is one event, not two.
	_ = r.mgr.Create(r.session("sleep"))
	got := r.refusals()
	if len(got) != 1 {
		t.Fatalf("%d container-refused events, want 1", len(got))
	}
	if !strings.Contains(got[0].Message, "seat") || !strings.Contains(got[0].Message, "container_image") {
		t.Errorf("the event does not name the role and the key: %q", got[0].Message)
	}
	if n := len(r.ring.Snapshot(events.Filter{Kind: events.KindSessionPlacementRefused}, 0)); n != 0 {
		t.Errorf("%d workdir placement-refused events; the container refusal has its own kind", n)
	}
	standing := r.mgr.PlacementRefusals()
	if len(standing) != 1 || standing[0].Reason != RefusalContainerImage || standing[0].Role != "seat" {
		t.Errorf("standing refusals = %+v, want one with reason %q for role seat", standing, RefusalContainerImage)
	}
	r.mgr.ClearPlacementRefusal(r.ws, "squad", "seat")
	if n := len(r.mgr.PlacementRefusals()); n != 0 {
		t.Errorf("%d standing refusals after the controller cleared the role", n)
	}
}

// With an image the prepared command runs under the runtime: the image and the
// mounts are arguments, env goes by name, and the seat's own command runs.
func TestContainerPlacementWithAnImageRunsUnderTheRuntime(t *testing.T) {
	r := newContainerRig(t, "test-ctr-img", api.PlacementContainer, api.Role{Name: "seat", Replicas: 1, ContainerImage: "registry.example/pi:1"})
	sess := r.session("sleep")
	if err := r.mgr.Create(sess); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !r.ran(t) {
		t.Fatal("the seat's command never ran")
	}
	calls := strings.Join(r.calls(), "\n")
	for _, want := range []string{"--- rm", "--- run", "--rm", "--init", "-it", "registry.example/pi:1", r.work + ":" + r.work, "-e\nMARVEL_SESSION"} {
		if !strings.Contains(calls, want) {
			t.Errorf("the runtime calls lack %q:\n%s", want, calls)
		}
	}
	if strings.Contains(calls, "MARVEL_SESSION=") {
		t.Errorf("an env value reached the command line:\n%s", calls)
	}
	if n := len(r.refusals()); n != 0 {
		t.Errorf("%d refusal events for a seat with an image", n)
	}
	if _, err := r.store.GetSession(sess.Key()); err != nil {
		t.Errorf("no session record: %v", err)
	}
}

// The image reaches the command through containerQuote, so an image reference
// with a shell character is refused before anything starts rather than parsed.
func TestContainerImageThatIsNotPlainIsRefusedAtLaunch(t *testing.T) {
	r := newContainerRig(t, "test-ctr-badimg", api.PlacementContainer, api.Role{Name: "seat", Replicas: 1, ContainerImage: "img;touch /tmp/x"})
	err := r.mgr.Create(r.session("sleep"))
	if err == nil {
		t.Fatal("Create accepted an image reference with a shell character")
	}
	if !r.neverRan(t) {
		t.Error("the seat started")
	}
	if len(r.calls()) != 0 {
		t.Errorf("the container runtime was called: %v", r.calls())
	}
}

// An explicit localhost beats the harness's recommendation: pi on a workspace
// that says localhost runs on the host, with no image and no refusal.
func TestExplicitLocalhostRunsAContainerRecommendingHarnessOnTheHost(t *testing.T) {
	r := newContainerRig(t, "test-ctr-local", api.PlacementLocalhost, api.Role{Name: "seat", Replicas: 1})
	if err := r.mgr.Create(r.session("pi")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !r.ran(t) {
		t.Fatal("the seat did not run on the host")
	}
	if len(r.calls()) != 0 || len(r.refusals()) != 0 {
		t.Errorf("localhost placement touched the runtime or refused: %v %v", r.calls(), r.refusals())
	}
}

// A harness that recommends a sandbox, on a workspace that names no placement,
// resolves to container, and with no image it is refused.
func TestARecommendedContainerWithNoImageIsRefused(t *testing.T) {
	r := newContainerRig(t, "test-ctr-rec", "", api.Role{Name: "seat", Replicas: 1})
	err := r.mgr.Create(r.session("pi"))
	if !errors.Is(err, ErrPlacementRefused) {
		t.Fatalf("Create = %v, want a placement refusal", err)
	}
	if !r.neverRan(t) {
		t.Error("pi started on the host")
	}
	// And a harness with no recommendation, same workspace, runs on the host.
	r2 := newContainerRig(t, "test-ctr-rec2", "", api.Role{Name: "seat", Replicas: 1})
	if err := r2.mgr.Create(r2.session("sleep")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !r2.ran(t) {
		t.Error("a harness with no recommendation did not run on the host")
	}
}

// No path starts a container-placed seat on the host. With an image set and no
// container runtime installed the launch fails: no record, no pane, no call.
// What the operator wants there (refuse with a reason, or fall back) is the open
// card sandbox-refusal-vs-d2; this pins only that it never starts on the host.
func TestContainerPlacementWithNoRuntimeNeverStartsOnTheHost(t *testing.T) {
	r := newContainerRig(t, "test-ctr-noruntime", api.PlacementContainer, api.Role{Name: "seat", Replicas: 1, ContainerImage: "img:1"})
	r.mgr.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	sess := r.session("sleep")
	if err := r.mgr.Create(sess); err == nil {
		t.Fatal("Create succeeded with no container runtime")
	}
	if _, err := r.store.GetSession(sess.Key()); err == nil {
		t.Error("a failed launch left a session record")
	}
	if !r.neverRan(t) {
		t.Error("the seat started on the host")
	}
}

// A session with no team record (an ad-hoc run) is placed by the same rule: it
// has no role to carry an image, so a container placement refuses it, and the
// raw-command fallback never reaches the host.
func TestAdHocSessionInAContainerWorkspaceIsRefusedNotStartedOnTheHost(t *testing.T) {
	r := newContainerRig(t, "test-ctr-adhoc", api.PlacementContainer, api.Role{Name: "seat", Replicas: 1})
	sess := r.session("sleep")
	sess.Team, sess.Role = "adhoc", "adhoc"
	err := r.mgr.Create(sess)
	if !errors.Is(err, ErrPlacementRefused) {
		t.Fatalf("Create = %v, want a placement refusal", err)
	}
	if !r.neverRan(t) {
		t.Error("the ad-hoc session started on the host")
	}
}

// Once the role names an image the standing refusal goes without anyone
// clearing it, and the seat starts.
func TestAddingTheImageEndsTheRefusal(t *testing.T) {
	r := newContainerRig(t, "test-ctr-fix", api.PlacementContainer, api.Role{Name: "seat", Replicas: 1})
	if err := r.mgr.Create(r.session("sleep")); !errors.Is(err, ErrPlacementRefused) {
		t.Fatalf("first Create = %v", err)
	}
	if err := r.store.UpdateTeam("test-ctr-fix/squad", func(tm *api.Team) error {
		tm.Roles[0].ContainerImage = "img:1"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.mgr.Create(r.session("sleep")); err != nil {
		t.Fatalf("Create after the image was set: %v", err)
	}
	if !r.ran(t) {
		t.Error("the seat did not start")
	}
	if n := len(r.mgr.PlacementRefusals()); n != 0 {
		t.Errorf("%d standing refusals after the refusal's cause went", n)
	}
}
