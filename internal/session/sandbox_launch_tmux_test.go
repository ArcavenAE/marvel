package session

import (
	"crypto/sha256"
	"encoding/hex"
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

// The spawn path, end to end, on a real tmux pane with a fake sandbox-exec: the
// fake logs its arguments and then runs the command after the options, as the real
// one does. No model and no real sandbox are involved.

type sandboxSpawnRig struct {
	*sandboxRig
	log string
	out string
}

func newSandboxSpawnRig(t *testing.T) *sandboxSpawnRig {
	t.Helper()
	skipIfNoTmux(t)
	r := newSandboxRig(t, api.Role{}, api.PlacementLocalhost)
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatal(err)
	}
	// The same seams, on a manager that can start panes.
	old := r.mgr
	mgr := NewManager(r.store, driver)
	mgr.SocketPath = filepath.Join(t.TempDir(), "marvel.sock")
	mgr.Events = old.Events
	mgr.GOOS, mgr.SandboxExec, mgr.SandboxDir, mgr.RealHome, mgr.HarnessHomeDir = old.GOOS, old.SandboxExec, old.SandboxDir, old.RealHome, old.HarnessHomeDir
	r.mgr = mgr
	out, _ := filepath.EvalSymlinks(t.TempDir())
	log := filepath.Join(out, "calls.log")
	// The seat's command records that it ran, then sleeps.
	writeFile(t, r.harness, "#!/bin/sh\npwd -P > "+out+"/ran\nexec sleep 300\n", 0o755)
	// The fake sandbox-exec logs every argument on its own line, skips -f F and
	// each -D K=V, and runs the rest.
	writeFile(t, r.exec, "#!/bin/sh\nfor a; do echo \"$a\" >> '"+log+"'; done\n"+
		"while [ $# -gt 0 ]; do case \"$1\" in -f|-D) shift 2;; *) break;; esac; done\nexec \"$@\"\n", 0o755)
	t.Cleanup(func() { _ = mgr.CleanupWorkspace("w") })
	return &sandboxSpawnRig{sandboxRig: r, log: log, out: out}
}

func (r *sandboxSpawnRig) waitRan(t *testing.T) bool {
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

// A profiled role starts under sandbox-exec: the seat's command runs behind the
// prefix, the prefix points at marvel's per-launch copy, and session.created
// carries that copy's path and hash in the fixed format.
func TestProfiledRoleSpawnsUnderSandboxExec(t *testing.T) {
	r := newSandboxSpawnRig(t)
	sess := r.session()
	if err := r.mgr.Create(sess); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !r.waitRan(t) {
		t.Fatal("the seat's command never ran behind sandbox-exec")
	}
	raw, err := os.ReadFile(r.log)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(args) < 12 || args[0] != "-f" {
		t.Fatalf("sandbox-exec arguments = %q", args)
	}
	copyPath := args[1]
	if filepath.Dir(copyPath) != r.copies {
		t.Errorf("-f names %q, not a copy under %q", copyPath, r.copies)
	}
	if copyPath == r.profile {
		t.Error("-f names the role's own profile path, not marvel's copy")
	}
	var params []string
	for i := 2; i+1 < len(args) && args[i] == "-D"; i += 2 {
		params = append(params, strings.SplitN(args[i+1], "=", 2)[0])
	}
	if strings.Join(params, ",") != "HARNESS_BIN,MARVEL_BIN,REALHOME,SESSION_DIR,WORKDIR" {
		t.Errorf("-D parameters = %v", params)
	}
	if args[len(args)-1] != r.harness && !strings.HasSuffix(strings.Join(args, " "), r.harness) {
		t.Errorf("the seat's command is not what follows the options: %q", args)
	}
	created := r.ring.Snapshot(events.Filter{Kind: events.KindSessionCreated}, 0)
	if len(created) != 1 {
		t.Fatalf("%d session.created events", len(created))
	}
	p, sha, ok := ParseSandboxEventSuffix(created[0].Message)
	if !ok || p != copyPath {
		t.Fatalf("session.created = %q, want the suffix for %q", created[0].Message, copyPath)
	}
	body, _ := os.ReadFile(copyPath)
	sum := sha256.Sum256(body)
	if sha != hex.EncodeToString(sum[:]) {
		t.Errorf("the event's sha is not the hash of the copy sandbox-exec read")
	}
	if r.mgr.PlacementRefusals() != nil && len(r.mgr.PlacementRefusals()) != 0 {
		t.Errorf("standing refusals: %+v", r.mgr.PlacementRefusals())
	}
}

// A refused profiled spawn leaves no session record, starts nothing, and is one
// event however often the reconcile loop retries.
func TestProfiledRoleRefusedAtSpawnStartsNothing(t *testing.T) {
	r := newSandboxSpawnRig(t)
	r.mgr.GOOS = "linux"
	sess := r.session()
	err := r.mgr.Create(sess)
	if !errors.Is(err, ErrPlacementRefused) {
		t.Fatalf("Create = %v, want an error wrapping ErrPlacementRefused", err)
	}
	if _, gerr := r.store.GetSession(sess.Key()); gerr == nil {
		t.Error("a refused spawn left a session record")
	}
	time.Sleep(1500 * time.Millisecond)
	if _, serr := os.Stat(filepath.Join(r.out, "ran")); serr == nil {
		t.Error("the seat started on the host")
	}
	_ = r.mgr.Create(r.session())
	if n := len(r.refusedEvents()); n != 1 {
		t.Errorf("%d sandbox-refused events, want 1", n)
	}
	r.mgr.ClearPlacementRefusal("w", "squad", "seat")
	if n := len(r.mgr.PlacementRefusals()); n != 0 {
		t.Errorf("%d standing refusals after the controller cleared the role", n)
	}
}
