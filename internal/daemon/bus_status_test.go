package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/bus"
)

func TestBusStatusWithoutABusSaysSo(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := d.dispatchAs(Request{Method: "bus.status"}, localCaller())
	if resp.Error == "" || !strings.Contains(resp.Error, "no bus is configured") {
		t.Fatalf("resp = %+v, want a no-bus error", resp)
	}
}

func TestBusStatusReportsAnAdoptedURL(t *testing.T) {
	d := newHandlerDaemon(t)
	d.sessMgr.Bus = bus.NewAdopted("nats://h:4222")
	resp := d.dispatchAs(Request{Method: "bus.status"}, localCaller())
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
}

func TestBusStatusIsNotACredentialPushMethod(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := d.dispatchAs(Request{Method: "bus.status"}, caller{scope: ScopeCredentialPush, fingerprint: "SHA256:x"})
	if resp.Error == "" || !strings.Contains(resp.Error, "not permitted") {
		t.Fatalf("credential-push key reached bus.status: %+v", resp)
	}
}

func TestStopParamsCarryKeepBus(t *testing.T) {
	var p stopParams
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
