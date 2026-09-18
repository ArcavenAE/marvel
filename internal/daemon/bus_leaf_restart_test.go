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

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/bus"
	"github.com/arcavenae/marvel/internal/config"
)

// startTestLeafBroker wires a managed broker the way attachBus does and starts
// it: a hub-configured cluster, the seed revealed from the store at each spawn,
// and provisioning as AfterReady. With preEnroll the seed is in the store
// before the broker starts, so it boots with the seed in its environment and
// the leaf attached (no enrollment restart); without it the broker boots
// unenrolled. Returns the supervisor and the rendered-config directory. Skips
// when nats-server is not on PATH.
func startTestLeafBroker(t *testing.T, d *Daemon, hub string, preEnroll bool) (*bus.Supervisor, string) {
	t.Helper()
	if _, err := exec.LookPath("nats-server"); err != nil {
		t.Skip("nats-server not on PATH")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "state", "nats")
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listen := l.Addr().String()
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	if preEnroll {
		kp, _ := nkeys.CreateUser()
		seed, _ := kp.Seed()
		if err := d.store.CreateCredential(&api.Credential{
			Name: busLeafCredential, Kind: api.CredentialNATSNKeySeed, Value: seed, IssuedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	rb := config.ResolvedBus{Managed: true, Listen: listen, URL: "nats://" + listen, StoreDir: dir, HubURL: hub}
	mgr, err := bus.NewManager(dir, "t"+strconv.Itoa(port), rb, d.store, func() bool {
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
	sup.Env = func() []string {
		seed, err := d.store.RevealCredentialValue(busLeafCredential)
		if err != nil {
			return nil
		}
		return []string{bus.LeafSeedEnv + "=" + string(seed)}
	}
	// A bare broker is not ready under the structural-health contract
	// (aae-orc-vy6k7), so provision as the daemon does.
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
	d.busSup = sup
	t.Cleanup(func() { sup.Stop(false) })
	return sup, dir
}

func leafInConf(t *testing.T, dir string) bool {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, bus.ConfName))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(body), "leafnodes")
}

// TestLeafCredentialPutRestartsOnFirstEnrollmentThenReloads is the enrollment
// lifecycle end to end inside the daemon: a hub-configured cluster starts
// unenrolled, the first `credential put bus/leaf` restarts the broker so its
// environment carries the seed (the one accepted bounce), and a later put on
// the same broker is reload-only because the environment already carries the
// seed (aae-orc-ct0l4). Needs a real nats-server.
func TestLeafCredentialPutRestartsOnFirstEnrollmentThenReloads(t *testing.T) {
	d := newHandlerDaemon(t)
	sup, _ := startTestLeafBroker(t, d, "nats-leaf://127.0.0.1:1", false)
	first := sup.Status()
	if first.Leaf != "unenrolled" {
		t.Fatalf("before put: leaf=%q, want unenrolled", first.Leaf)
	}

	kp, _ := nkeys.CreateUser()
	seed, _ := kp.Seed()
	params, _ := json.Marshal(map[string]any{"name": busLeafCredential, "kind": "nats-nkey-seed", "value": seed})
	resp := d.dispatchAs(Request{Method: "credential.put", Params: params}, localCaller())
	if resp.Error != "" {
		t.Fatalf("credential.put: %s", resp.Error)
	}
	after := sup.Status()
	if after.PID == first.PID || !after.Ready {
		t.Fatalf("broker not restarted by the first put: before %+v after %+v", first, after)
	}
	if after.Leaf == "unenrolled" {
		t.Error("leaf still unenrolled after the put")
	}

	// Delete clears memory only (brief 9 E4): the conf loses the remote and
	// reloads, but the running broker is not restarted for a revoke the hub
	// side owns. The seed stays in the broker's environment (delete does not
	// change the running process), so a second put is reload-only.
	del, _ := json.Marshal(map[string]string{"name": busLeafCredential})
	if resp := d.dispatchAs(Request{Method: "credential.delete", Params: del}, localCaller()); resp.Error != "" {
		t.Fatalf("credential.delete: %s", resp.Error)
	}
	if st := sup.Status(); st.PID != after.PID || !st.Ready || st.Leaf != "unenrolled" {
		t.Errorf("after delete: %+v, want same pid, ready, unenrolled", st)
	}
	if resp := d.dispatchAs(Request{Method: "credential.put", Params: params}, localCaller()); resp.Error != "" {
		t.Fatalf("second put: %s", resp.Error)
	}
	// The environment already carried the seed (the first put restarted for
	// it), so the second put reloads in place: same pid, and no longer
	// unenrolled. The link itself stays down because the hub port is bogus.
	if again := sup.Status(); again.PID != after.PID || !again.Ready || again.Leaf == "unenrolled" {
		t.Errorf("second put should be reload-only and re-enrolled: %+v", again)
	}
}

// TestBusLeafConnectDisconnectIsReloadOnly is the connect/disconnect lifecycle:
// against a broker that booted with the seed enrolled, disconnect and connect
// toggle the leafnodes block and reload without ever bouncing the broker, so
// every local session survives (aae-orc-ct0l4). Needs a real nats-server.
func TestBusLeafConnectDisconnectIsReloadOnly(t *testing.T) {
	d := newHandlerDaemon(t)
	sup, dir := startTestLeafBroker(t, d, "nats-leaf://127.0.0.1:1", true)
	start := sup.Status()
	switch start.Leaf {
	case "unenrolled", "n/a", "detached":
		t.Fatalf("pre-enrolled attached broker: leaf=%q, want up/down/unknown", start.Leaf)
	}
	if !leafInConf(t, dir) {
		t.Fatal("pre-enrolled attached broker rendered no leaf block")
	}
	pid := start.PID

	// Disconnect: reload-only, the broker never bounces, the block is gone.
	if resp := d.dispatchAs(Request{Method: "bus.leaf.disconnect"}, localCaller()); resp.Error != "" {
		t.Fatalf("disconnect: %s", resp.Error)
	}
	off := sup.Status()
	if off.PID != pid {
		t.Errorf("disconnect bounced the broker: pid %d -> %d", pid, off.PID)
	}
	if !off.Ready || off.Leaf != "detached" {
		t.Errorf("after disconnect: %+v, want ready and detached", off)
	}
	if leafInConf(t, dir) {
		t.Error("disconnect left the leaf block in the conf")
	}

	// Reconnect: reload-only, same broker, the block is back, no re-enrollment.
	if resp := d.dispatchAs(Request{Method: "bus.leaf.connect"}, localCaller()); resp.Error != "" {
		t.Fatalf("connect: %s", resp.Error)
	}
	on := sup.Status()
	if on.PID != pid {
		t.Errorf("connect bounced the broker: pid %d -> %d", pid, on.PID)
	}
	if !on.Ready || on.Leaf == "detached" || on.Leaf == "unenrolled" {
		t.Errorf("after connect: %+v, want ready and re-attached", on)
	}
	if !leafInConf(t, dir) {
		t.Error("connect did not restore the leaf block")
	}
}
