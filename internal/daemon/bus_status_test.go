package daemon

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/bus"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

func TestBusStatusWithoutABusSaysSo(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := d.dispatchAs(daemonrpc.Request{Method: "bus.status"}, localCaller())
	if resp.Error != daemonrpc.NoBusReply || !strings.Contains(resp.Error, "no bus is configured") {
		t.Fatalf("resp = %+v, want a no-bus error", resp)
	}
}

func TestBusStatusReportsAnAdoptedURL(t *testing.T) {
	d := newHandlerDaemon(t)
	d.sessMgr.Bus = bus.NewAdopted(config.ResolvedBus{Mode: "adopted", Class: "message-bus", Provider: "nats-server", URL: "nats://h:4222"})
	resp := d.dispatchAs(daemonrpc.Request{Method: "bus.status"}, localCaller())
	if resp.Error != "" {
		t.Fatal(resp.Error)
	}
	var st bus.Status
	if err := json.Unmarshal(resp.Result, &st); err != nil {
		t.Fatal(err)
	}
	if st.Managed || st.URL != "nats://h:4222" || st.Leaf != "n/a" {
		t.Errorf("status = %+v", st)
	}
	if st.Class != "message-bus" || st.Provider != "nats-server" || st.Mode != "adopted" {
		t.Errorf("adopted status lacks the record fields: %+v", st)
	}
}

// An adopted bus whose cluster declares a hub reports the leaf it reads from
// its loopback monitor, so the post-check can tell it from a hubless one
// (aae-orc-c6o29).
func TestBusStatusReadsTheLeafOfAnAdoptedBusWithAHub(t *testing.T) {
	var hits int
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"leafnodes":1}`))
	}))
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	mon := l.Addr().(*net.TCPAddr).Port
	if mon <= 4000 {
		t.Skipf("monitor port %d is too low to place a client port below it", mon)
	}
	listen := "127.0.0.1:" + strconv.Itoa(mon-4000)

	d := newHandlerDaemon(t)
	d.sessMgr.Bus = bus.NewAdopted(config.ResolvedBus{
		Mode: "adopted", Class: "message-bus", Provider: "nats-server",
		Listen: listen, URL: "nats://" + listen,
		HubURL: "nats-leaf://hub.example.test:7442", HubURLs: []string{"nats-leaf://hub.example.test:7442"},
	})
	resp := d.dispatchAs(daemonrpc.Request{Method: "bus.status"}, localCaller())
	if resp.Error != "" {
		t.Fatal(resp.Error)
	}
	var st bus.Status
	if err := json.Unmarshal(resp.Result, &st); err != nil {
		t.Fatal(err)
	}
	if st.Managed || !st.HubDeclared || st.Leaf != "up" || st.LeafValidUntil == "" {
		t.Errorf("status = {Managed:%v HubDeclared:%v Leaf:%q ValidUntil:%q}, want an unmanaged bus with a declared hub, its leaf up and a dated reading", st.Managed, st.HubDeclared, st.Leaf, st.LeafValidUntil)
	}
	if hits != 1 {
		t.Errorf("monitor reads = %d, want 1", hits)
	}
}

func TestBusStatusIsNotACredentialPushMethod(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := d.dispatchAs(daemonrpc.Request{Method: "bus.status"}, caller{scope: ScopeCredentialPush, fingerprint: "SHA256:x"})
	if resp.Error == "" || !strings.Contains(resp.Error, "not permitted") {
		t.Fatalf("credential-push key reached bus.status: %+v", resp)
	}
}

func TestBusLeafToggleReusesTheCredentialPushGate(t *testing.T) {
	d := newHandlerDaemon(t)
	for _, m := range []string{"bus.leaf.connect", "bus.leaf.disconnect"} {
		resp := d.dispatchAs(daemonrpc.Request{Method: m}, caller{scope: ScopeCredentialPush, fingerprint: "SHA256:x"})
		// The toggle reuses credential.put/delete's gate (aae-orc-ct0l4), so the
		// scope does not refuse; without a managed bus the handler answers.
		if strings.Contains(resp.Error, "not permitted") {
			t.Errorf("%s refused for a credential-push key: %s", m, resp.Error)
		}
		if !strings.Contains(resp.Error, "no managed bus") {
			t.Errorf("%s without a managed bus: error = %q, want the handler's no-managed-bus answer", m, resp.Error)
		}
	}
}

func TestStopParamsCarryKeepBus(t *testing.T) {
	var p daemonrpc.StopParams
	if err := json.Unmarshal([]byte(`{"keep_bus":true}`), &p); err != nil {
		t.Fatal(err)
	}
	if !p.KeepBus || p.Teardown {
		t.Errorf("params = %+v", p)
	}
	if teardown, mode, err := stopMode([]byte(`{"keep_bus":true}`)); err != nil || teardown || mode != "detach" {
		t.Errorf("stopMode with keep_bus: teardown=%v mode=%q err=%v", teardown, mode, err)
	}
}
