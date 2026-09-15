package daemon

import (
	"context"
	"encoding/json"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/nats-io/nkeys"

	"github.com/arcavenae/marvel/internal/bus"
	"github.com/arcavenae/marvel/internal/config"
)

// TestLeafCredentialPutRestartsTheSupervisedBroker is brief 10 s.6 step 7
// end to end inside the daemon: a hub-configured cluster starts unenrolled,
// `credential put bus/leaf` re-renders the leaf remote, and the broker is
// restarted so its environment carries the seed. Needs a real nats-server.
func TestLeafCredentialPutRestartsTheSupervisedBroker(t *testing.T) {
	if _, err := exec.LookPath("nats-server"); err != nil {
		t.Skip("nats-server not on PATH")
	}
	d := newHandlerDaemon(t)
	root := t.TempDir()
	dir := filepath.Join(root, "state", "nats")
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listen := l.Addr().String()
	_ = l.Close()
	rb := config.ResolvedBus{Managed: true, Listen: listen, URL: "nats://" + listen, StoreDir: dir, HubURL: "nats-leaf://127.0.0.1:1"}
	mgr, err := bus.NewManager(dir, "t"+strconv.Itoa(l.Addr().(*net.TCPAddr).Port), rb, d.store, func() bool {
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
	d.bus, d.sessMgr.Bus = mgr, mgr
	d.regenerateBus("start")
	mgr.Reloader = sup
	if err := sup.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	d.busSup = sup
	t.Cleanup(func() { sup.Stop(false) })
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
		t.Fatalf("broker not restarted by the put: before %+v after %+v", first, after)
	}
	if after.Leaf == "unenrolled" {
		t.Error("leaf still unenrolled after the put")
	}

	// Delete clears memory only (brief 9 E4): the conf loses the remote and
	// reloads, but the running broker is not restarted for a revoke the hub
	// side owns. A fresh put is a rendered change again, and restarts.
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
	if again := sup.Status(); again.PID == after.PID || !again.Ready {
		t.Errorf("second put did not restart: %+v", again)
	}
}
