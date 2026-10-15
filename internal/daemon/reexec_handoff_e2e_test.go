package daemon

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nkeys"

	"github.com/arcavenae/marvel/internal/bus"
	"github.com/arcavenae/marvel/internal/events"
)

// The end-to-end side of aae-orc-4h9xp, against a real nats-server: a leafed
// broker, a departing daemon that carries the seed, and a successor that adopts
// the broker. Every seed is minted for the test.

// adoptLeafBroker builds the supervisor a successor daemon d would run over the
// layout dir the first daemon used, wired as attachBus wires it, and starts it.
func adoptLeafBroker(t *testing.T, d, first *Daemon, dir string) *bus.Supervisor {
	t.Helper()
	root := filepath.Dir(filepath.Dir(dir))
	mgr, err := bus.NewManager(dir, first.bus.Domain(), first.bus.Bus(), d.store, func() bool {
		_, gerr := d.store.GetCredential(busLeafCredential)
		return gerr == nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sup, err := bus.NewSupervisor(mgr, filepath.Join(root, "run"), filepath.Join(root, "log"), d.events)
	if err != nil {
		t.Fatal(err)
	}
	sup.SetDialTimeout(30 * time.Second)
	sup.SetLeafLossCause(d.leafLossCause)
	sup.Env = func() []string {
		seed, rerr := d.store.RevealCredentialValue(busLeafCredential)
		if rerr != nil {
			return nil
		}
		return []string{bus.LeafSeedEnv + "=" + string(seed)}
	}
	sup.AfterReady = func() error {
		admin := mgr.Admin()
		_, perr := bus.Provision(context.Background(), mgr.URL(), admin.Name, admin.Password)
		return perr
	}
	d.bus, d.sessMgr.Bus = mgr, mgr
	d.regenerateBus("start")
	mgr.Reloader = sup
	if err := sup.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	d.lifecycle = bus.NewLifecycle(sup)
	t.Cleanup(func() { sup.Stop(false) })
	return sup
}

// carryAcross runs the departing side through the exec seam and returns the
// descriptor number the successor would inherit.
func carryAcross(t *testing.T, d *Daemon) (fd int, envv []string) {
	t.Helper()
	d.handoffProbe = capableTarget
	var r reexecSeam
	d.seamReexec(&r)
	if err := d.ReexecTo("/bin/marvel-target"); err != nil {
		t.Fatal(err)
	}
	return fdFromEnv(t, r.envv), r.envv
}

func readAllFiles(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			if body, rerr := os.ReadFile(p); rerr == nil {
				b.Write(body)
			}
		}
		return nil
	})
	return b.String()
}

func TestACarriedLeafSeedSurvivesAReexecWithTheBrokerUntouched(t *testing.T) {
	logs := captureLog(t)
	d1 := newHandlerDaemon(t)
	sup1, dir := startTestLeafBroker(t, d1, "nats-leaf://127.0.0.1:1", true)
	seedBytes, err := d1.store.RevealCredentialValue(busLeafCredential)
	if err != nil {
		t.Fatal(err)
	}
	seed := string(seedBytes)
	pid := sup1.Status().PID
	if !leafInConf(t, dir) {
		t.Fatal("the first daemon's conf has no leafnodes block: nothing to keep")
	}

	fd, envv := carryAcross(t, d1)
	if d1.events.Snapshot(events.Filter{Kind: events.KindCredentialTransientDropped}, 0) != nil {
		t.Error("the departing daemon called a carried seed dropped")
	}

	// The successor: reads the pipe first, restores the store, then adopts.
	t.Setenv(handoffEnvVar, strconv.Itoa(fd))
	d2 := newHandlerDaemon(t)
	if _, err := d2.store.GetCredential(busLeafCredential); err != nil {
		t.Fatalf("the successor's store lacks bus/leaf after New: %v", err)
	}
	sup2 := adoptLeafBroker(t, d2, d1, dir)

	st := sup2.Status()
	if !st.Adopted || st.PID != pid {
		t.Errorf("successor status = adopted %v pid %d, want adopted pid %d", st.Adopted, st.PID, pid)
	}
	if st.LeafSource != "store" || st.Leaf == "unenrolled" {
		t.Errorf("leaf = %q, source = %q, want the store's seed to hold the link", st.Leaf, st.LeafSource)
	}
	if !leafInConf(t, dir) {
		t.Error("the successor rendered the conf without a leafnodes block: the adopt SIGHUP removes the remote")
	}
	if evs := d2.events.Snapshot(events.Filter{Kind: events.KindBusLeafDropped}, 0); len(evs) != 0 {
		t.Errorf("bus.leaf.dropped fired %d times for a carried seed", len(evs))
	}
	if body := readAllFiles(t, filepath.Join(filepath.Dir(filepath.Dir(dir)), "log")); strings.Contains(body, "removed") {
		t.Errorf("the broker's log says a leaf remote was removed:\n%s", body)
	}

	// A later put of the same seed reloads in place: the adopt restored what the
	// broker was spawned with, so the daemon does not bounce it.
	del, _ := json.Marshal(map[string]string{"name": busLeafCredential})
	if resp := d2.dispatchAs(Request{Method: "credential.delete", Params: del}, localCaller()); resp.Error != "" {
		t.Fatalf("credential.delete: %s", resp.Error)
	}
	put, _ := json.Marshal(map[string]any{"name": busLeafCredential, "kind": "nats-nkey-seed", "value": seedBytes})
	if resp := d2.dispatchAs(Request{Method: "credential.put", Params: put}, localCaller()); resp.Error != "" {
		t.Fatalf("credential.put: %s", resp.Error)
	}
	if again := sup2.Status(); again.PID != pid || !again.Ready {
		t.Errorf("a put of the same seed after the adopt restarted the broker: %+v, want pid %d", again, pid)
	}

	// The canary: the seed's bytes are in no log, event, sidecar, status,
	// credential read, conf, environment value or file the daemons wrote.
	root := filepath.Dir(filepath.Dir(dir))
	surfaces := map[string]string{
		"the log":                  logs.String(),
		"the passed environment":   strings.Join(envv, "\n"),
		"the departing events":     eventsText(d1.events.Snapshot(events.Filter{}, 0)),
		"the successor's events":   eventsText(d2.events.Snapshot(events.Filter{}, 0)),
		"the sidecar and the pids": readAllFiles(t, filepath.Join(root, "run")),
		"the conf and its state":   readAllFiles(t, dir),
	}
	for _, method := range []string{"bus.status", "credential.list"} {
		resp := d2.dispatchAs(Request{Method: method}, localCaller())
		if resp.Error != "" || len(resp.Result) == 0 {
			t.Errorf("%s gave no answer to search (%q), so its check would be empty", method, resp.Error)
		}
		surfaces[method] = string(resp.Result) + resp.Error
	}
	get, _ := json.Marshal(map[string]string{"name": busLeafCredential})
	resp := d2.dispatchAs(Request{Method: "credential.get", Params: get}, localCaller())
	if resp.Error != "" || len(resp.Result) == 0 {
		t.Errorf("credential.get gave no answer to search (%q), so its check would be empty", resp.Error)
	}
	surfaces["credential.get"] = string(resp.Result) + resp.Error
	for name, text := range surfaces {
		if strings.Contains(text, seed) {
			t.Errorf("%s carries the seed's bytes", name)
		}
	}
}

func eventsText(evs []events.Event) string {
	var b strings.Builder
	for _, e := range evs {
		body, _ := json.Marshal(e)
		b.Write(body)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestAnUnawareSuccessorFiresTheDroppedSignalAndStatusSaysSo(t *testing.T) {
	d1 := newHandlerDaemon(t)
	_, dir := startTestLeafBroker(t, d1, "nats-leaf://127.0.0.1:2", true)
	// The seam skips the read: nothing is carried to a successor that never asks.
	d1.handoffProbe = func(string) error { return os.ErrPermission }
	var r reexecSeam
	d1.seamReexec(&r)
	if err := d1.ReexecTo("/bin/marvel-target"); err != nil {
		t.Fatal(err)
	}
	if hasHandoffVar(r.envv) {
		t.Fatal("a declined handoff set the variable")
	}

	d2 := newHandlerDaemon(t)
	sup2 := adoptLeafBroker(t, d2, d1, dir)
	if leafInConf(t, dir) {
		t.Error("an empty store rendered a leafnodes block")
	}
	evs := d2.events.Snapshot(events.Filter{Kind: events.KindBusLeafDropped}, 0)
	if len(evs) != 1 || evs[0].Severity != events.SeverityError {
		t.Fatalf("bus.leaf.dropped events = %+v, want one at error severity", evs)
	}
	resp := d2.dispatchAs(Request{Method: "bus.status"}, localCaller())
	var st bus.Status
	if err := json.Unmarshal(resp.Result, &st); err != nil {
		t.Fatal(err)
	}
	if st.LeafSource != "dropped-on-adopt" || sup2.Status().LeafSource != "dropped-on-adopt" {
		t.Errorf("bus.status leaf_source = %q, want dropped-on-adopt", st.LeafSource)
	}
}

func TestNewReadsTheHandoffBeforeAnythingElseAndFlagsAnUnreadableOne(t *testing.T) {
	src := newHandlerDaemon(t)
	putDummy(t, src, busLeafCredential, handoffCanary)
	fd, _ := carryAcross(t, src)

	t.Setenv(handoffEnvVar, strconv.Itoa(fd))
	d, err := New()
	if err != nil {
		t.Fatal(err)
	}
	got, gerr := d.store.RevealCredentialValue(busLeafCredential)
	if gerr != nil || string(got) != handoffCanary {
		t.Errorf("New did not restore bus/leaf: %q, %v", got, gerr)
	}
	if d.leafLossCause != "" {
		t.Errorf("leafLossCause = %q after a good handoff, want none", d.leafLossCause)
	}
	if _, set := os.LookupEnv(handoffEnvVar); set {
		t.Error("New left the handoff variable set")
	}
	if !isClosed(fd) {
		t.Error("New left the handoff descriptor open")
	}

	// An announced handoff that cannot be read: nothing partial, and the
	// successor remembers why for the signal it may have to raise.
	t.Setenv(handoffEnvVar, "407")
	d2, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(d2.store.ListCredentials()); n != 0 {
		t.Errorf("an unreadable handoff restored %d credentials", n)
	}
	if d2.leafLossCause != "handoff unread" {
		t.Errorf("leafLossCause = %q, want %q", d2.leafLossCause, "handoff unread")
	}
}

// Between prepare and the exec the daemon is still serving for 100ms and may
// spawn a child. The pipe must not ride into it.
func TestAChildSpawnedBetweenPrepareAndExecInheritsNoHandoffDescriptor(t *testing.T) {
	d := newHandlerDaemon(t)
	putDummy(t, d, "bus/leaf", handoffCanary)
	d.handoffProbe = capableTarget
	h := d.prepareHandoff("/bin/marvel-target")
	defer h.abort()
	if h.Outcome() != "carried" {
		t.Fatalf("outcome = %q", h.Outcome())
	}
	// A control first: the child sees its own standard output, so the probe
	// can tell an open descriptor from a closed one.
	open := func(fd int) bool {
		return exec.Command("/bin/sh", "-c", "test -e /dev/fd/"+strconv.Itoa(fd)).Run() == nil
	}
	if !open(1) {
		t.Skip("this host's /dev/fd does not show a child's open descriptors")
	}
	if h.rfd < 3 {
		t.Fatalf("read end is descriptor %d", h.rfd)
	}
	if open(h.rfd) {
		t.Errorf("a child inherited descriptor %d, the read end of the handoff pipe", h.rfd)
	}
	// The positive control: once armed for the exec the same probe must see it,
	// or the check above proves nothing.
	if _, err := h.arm(); err != nil {
		t.Fatal(err)
	}
	if !open(h.rfd) {
		t.Errorf("the probe cannot see descriptor %d even after close-on-exec is cleared; the check proves nothing", h.rfd)
	}
}

// The cause the successor can name reaches the supervisor through attachBus,
// the path production takes: an announced handoff that cannot be read.
func TestAttachBusHandsTheUnreadCauseToTheSupervisor(t *testing.T) {
	if _, err := exec.LookPath("nats-server"); err != nil {
		t.Skip("nats-server not on PATH")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	d1 := newHandlerDaemon(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MARVEL_SOCKET", "")
	sock := filepath.Join(home, ".marvel", "run", "marvel.sock")
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "clusters:\n  - name: c1\n    socket: " + sock + "\n    bus:\n      managed: true\n      listen: 127.0.0.1:" +
		strconv.Itoa(port) + "\n      hub:\n        url: nats-leaf://127.0.0.1:1\ncurrent_cluster: c1\n"
	if err := os.WriteFile(filepath.Join(home, ".marvel", "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	kp, _ := nkeys.CreateUser()
	seed, _ := kp.Seed()
	putDummy(t, d1, busLeafCredential, string(seed))
	if err := d1.attachServices(sock); err != nil {
		t.Fatal(err)
	}
	d1.keepBus = true
	d1.stopBus(false)

	// The successor was told to read a handoff and cannot.
	t.Setenv(handoffEnvVar, "407")
	d2, err := New()
	if err != nil {
		t.Fatal(err)
	}
	// attachServices starts the broker, which adopts the one d1 left.
	if err := d2.attachServices(sock); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d2.stopBus(true) })
	evs := d2.events.Snapshot(events.Filter{Kind: events.KindBusLeafDropped}, 0)
	if len(evs) != 1 || !strings.Contains(evs[0].Message, "handoff unread") {
		t.Fatalf("bus.leaf.dropped events = %+v, want one that names the cause %q", evs, "handoff unread")
	}
}
