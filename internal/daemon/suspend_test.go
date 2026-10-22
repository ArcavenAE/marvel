package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/childproof"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/suspend"
)

// suspendDaemon is a daemon with a bolt store and nothing else: no controller,
// no tmux driver. suspend.status must answer from it all the same.
func suspendDaemon(t *testing.T) (*Daemon, string) {
	t.Helper()
	dir := t.TempDir()
	store := api.NewStore()
	if err := store.OpenBolt(filepath.Join(dir, "state.bolt")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseBolt() })
	return &Daemon{home: "/h", store: store}, filepath.Join(dir, "marvel.sock")
}

func suspendedRecord() suspend.Record {
	return suspend.Record{
		State: suspend.StateSuspended, Actor: "op", Source: "cli", Since: time.Now().UTC(),
		Server: suspend.Proc{Pid: 100, Identity: childproof.Identity{Start: "linux:b:1"}},
		Seats:  []suspend.Proc{{Pid: 200, Identity: childproof.Identity{Start: "linux:b:2"}}},
	}
}

func statusOf(t *testing.T, resp daemonrpc.Response) suspend.Status {
	t.Helper()
	if resp.Error != "" {
		t.Fatalf("suspend.status error: %s", resp.Error)
	}
	var st suspend.Status
	if err := json.Unmarshal(resp.Result, &st); err != nil {
		t.Fatal(err)
	}
	return st
}

// SR-B: suspend.status answers while the daemon is still adopting, from a
// daemon with no controller and no tmux driver, so it cannot be waiting on
// either.
func TestSuspendStatusAnswersWithoutControllerOrTmux(t *testing.T) {
	d, sock := suspendDaemon(t)
	d.loadSuspendRecord(sock)
	d.adoptingSince.Store(time.Now().UnixNano())
	if st := statusOf(t, d.dispatchAs(daemonrpc.Request{Method: "suspend.status"}, localCaller())); st.State != suspend.StateRunning {
		t.Errorf("state = %q with no record, want running", st.State)
	}
	if err := (suspendStore{d}).Save(suspendedRecord()); err != nil {
		t.Fatal(err)
	}
	st := statusOf(t, d.dispatchAs(daemonrpc.Request{Method: "suspend.status"}, localCaller()))
	if st.State != string(suspend.StateSuspended) || st.Record == nil || st.Record.Server.Pid != 100 {
		t.Errorf("status = %+v, want the suspended record", st)
	}
}

// Save writes bolt, the mirror and the live copy; Clear removes all three.
func TestSuspendStoreWritesThroughAndClears(t *testing.T) {
	d, sock := suspendDaemon(t)
	d.loadSuspendRecord(sock)
	mirror := suspend.MirrorPath(sock)
	if err := (suspendStore{d}).Save(suspendedRecord()); err != nil {
		t.Fatal(err)
	}
	if raw, _ := d.store.SuspendRecord(); raw == nil {
		t.Error("bolt has no record after Save")
	}
	if rec, found, err := suspend.ReadMirror(mirror); err != nil || !found || rec.State != suspend.StateSuspended {
		t.Errorf("mirror after Save: %+v found %v err %v", rec, found, err)
	}
	if err := (suspendStore{d}).Clear(); err != nil {
		t.Fatal(err)
	}
	if raw, _ := d.store.SuspendRecord(); raw != nil {
		t.Errorf("bolt after Clear: %s", raw)
	}
	if _, err := os.Stat(mirror); !os.IsNotExist(err) {
		t.Errorf("mirror after Clear: err %v, want gone", err)
	}
	if d.suspendRec.Load() != nil {
		t.Error("live copy after Clear is not nil")
	}
}

// At start the stored record becomes the live copy and the mirror is made to
// match it; a mirror left with no record behind it is removed.
func TestLoadSuspendRecordAtStart(t *testing.T) {
	d, sock := suspendDaemon(t)
	mirror := suspend.MirrorPath(sock)
	raw, err := json.Marshal(suspendedRecord())
	if err != nil {
		t.Fatal(err)
	}
	if err := d.store.PersistSuspendRecord(raw); err != nil {
		t.Fatal(err)
	}
	d.loadSuspendRecord(sock)
	if rec := d.suspendRec.Load(); rec == nil || rec.State != suspend.StateSuspended {
		t.Errorf("live copy = %+v, want the stored record", rec)
	}
	if _, found, _ := suspend.ReadMirror(mirror); !found {
		t.Error("the mirror was not written from the stored record")
	}

	d2, sock2 := suspendDaemon(t)
	stale := suspend.MirrorPath(sock2)
	if err := suspend.WriteMirror(stale, suspendedRecord()); err != nil {
		t.Fatal(err)
	}
	d2.loadSuspendRecord(sock2)
	if d2.suspendRec.Load() != nil {
		t.Error("a mirror alone became a live record")
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("a stale mirror with no record behind it was kept (err %v)", err)
	}
}

// A TCP listener has no mirror; the record still lives in bolt and memory.
func TestSuspendStoreOnTCPHasNoMirror(t *testing.T) {
	d, _ := suspendDaemon(t)
	d.loadSuspendRecord("127.0.0.1:7000")
	if d.suspendMirror != "" {
		t.Fatalf("mirror path %q for a TCP listener", d.suspendMirror)
	}
	if err := (suspendStore{d}).Save(suspendedRecord()); err != nil {
		t.Fatal(err)
	}
	if d.suspendRec.Load() == nil {
		t.Error("no live copy after Save on a TCP listener")
	}
}
