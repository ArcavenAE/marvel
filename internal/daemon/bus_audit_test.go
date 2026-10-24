package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/arcavenae/marvel/internal/bus"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

func auditCall(d *Daemon, seq uint64) daemonrpc.Response {
	params, _ := json.Marshal(map[string]uint64{"seq": seq})
	return d.dispatchAs(daemonrpc.Request{Method: "bus.audit", Params: params}, localCaller())
}

// auditStream opens the broker's audit stream as the admin identity.
func auditStream(t *testing.T, d *Daemon) (jetstream.JetStream, jetstream.Stream) {
	t.Helper()
	admin := d.bus.Admin()
	nc, err := nats.Connect(d.bus.URL(), nats.UserInfo(admin.Name, admin.Password))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	st, err := js.Stream(context.Background(), bus.AuditStream)
	if err != nil {
		t.Fatal(err)
	}
	return js, st
}

func publishAudit(t *testing.T, js jetstream.JetStream, hdr nats.Header, body string) uint64 {
	t.Helper()
	ack, err := js.PublishMsg(context.Background(), &nats.Msg{Subject: "agent.audit", Header: hdr, Data: []byte(body)})
	if err != nil {
		t.Fatal(err)
	}
	return ack.Sequence
}

func TestBusAuditReturnsTheRecord(t *testing.T) {
	d := newHandlerDaemon(t)
	startTestLeafBroker(t, d, "", false)
	js, _ := auditStream(t, d)
	hdr := nats.Header{}
	hdr.Set("Director-Outcome", "refused")
	hdr.Set("Director-Stage", "scope")
	hdr.Set("Director-Refusal", "not permitted")
	hdr.Set("Nats-Msg-Id", "abc-refused")
	hdr.Set("Director-Other", "must not be returned")
	body := `{"text":"hello"}`
	seq := publishAudit(t, js, hdr, body)

	resp := auditCall(d, seq)
	if resp.Error != "" {
		t.Fatal(resp.Error)
	}
	var rec bus.AuditRecord
	if err := json.Unmarshal(resp.Result, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Seq != seq || rec.Outcome != "refused" || rec.Stage != "scope" || rec.Refusal != "not permitted" || rec.MsgID != "abc-refused" {
		t.Errorf("record = %+v", rec)
	}
	if string(rec.Body) != body {
		t.Errorf("body = %q, want %q", rec.Body, body)
	}
	if strings.Contains(string(resp.Result), "must not be returned") {
		t.Errorf("a header outside the four came back: %s", resp.Result)
	}
}

func TestBusAuditRecordWithoutAnOutcomeHeader(t *testing.T) {
	d := newHandlerDaemon(t)
	startTestLeafBroker(t, d, "", false)
	js, _ := auditStream(t, d)
	seq := publishAudit(t, js, nil, `{"text":"hello"}`)
	resp := auditCall(d, seq)
	if resp.Error != "" {
		t.Fatal(resp.Error)
	}
	var rec bus.AuditRecord
	if err := json.Unmarshal(resp.Result, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != "" || rec.Stage != "" || rec.Refusal != "" {
		t.Errorf("record = %+v, want no director headers", rec)
	}
}

func TestBusAuditMissesEachSayTheirOwnThing(t *testing.T) {
	d := newHandlerDaemon(t)
	startTestLeafBroker(t, d, "", false)
	js, st := auditStream(t, d)

	if resp := auditCall(d, 1); resp.Error != "AGENT_AUDIT holds no records" {
		t.Errorf("empty stream: %q", resp.Error)
	}
	for i := 0; i < 5; i++ {
		publishAudit(t, js, nil, "r")
	}
	if resp := auditCall(d, 6); resp.Error != "seq 6 is past the last record, 5; check the number came from this cluster's audit line" {
		t.Errorf("past the end: %q", resp.Error)
	}
	if err := st.DeleteMsg(context.Background(), 3); err != nil {
		t.Fatal(err)
	}
	if resp := auditCall(d, 3); resp.Error != "seq 3 was deleted or purged; AGENT_AUDIT holds 1 to 5 with gaps" {
		t.Errorf("inside the range: %q", resp.Error)
	}
	for _, n := range []uint64{1, 2} {
		if err := st.DeleteMsg(context.Background(), n); err != nil {
			t.Fatal(err)
		}
	}
	if resp := auditCall(d, 2); resp.Error != "seq 2 has aged out; AGENT_AUDIT holds 4 to 5" {
		t.Errorf("below the first: %q", resp.Error)
	}
	if resp := auditCall(d, 0); resp.Error == "" || strings.Contains(resp.Error, "AGENT_AUDIT") {
		t.Errorf("seq 0: %q, want a refusal of the number", resp.Error)
	}
}

func TestAuditMissTextNamesEveryCase(t *testing.T) {
	cases := []struct {
		miss bus.AuditMiss
		want string
	}{
		{bus.AuditMiss{Kind: bus.AuditNoStream, Seq: 9}, "this broker has no AGENT_AUDIT stream"},
		{bus.AuditMiss{Kind: bus.AuditEmpty, Seq: 9}, "AGENT_AUDIT holds no records"},
		{bus.AuditMiss{Kind: bus.AuditAgedOut, Seq: 2, First: 4, Last: 8}, "seq 2 has aged out; AGENT_AUDIT holds 4 to 8"},
		{bus.AuditMiss{Kind: bus.AuditPastEnd, Seq: 9, First: 4, Last: 8}, "seq 9 is past the last record, 8; check the number came from this cluster's audit line"},
		{bus.AuditMiss{Kind: bus.AuditGap, Seq: 5, First: 4, Last: 8}, "seq 5 was deleted or purged; AGENT_AUDIT holds 4 to 8 with gaps"},
	}
	for _, c := range cases {
		if got := auditMissText(&c.miss); got != c.want {
			t.Errorf("kind %d: %q, want %q", c.miss.Kind, got, c.want)
		}
	}
}

func TestBusAuditWithoutAManagedBus(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := auditCall(d, 1)
	want := "no managed bus is configured for this cluster; marvel reads the audit log only where it runs the broker"
	if resp.Error != want {
		t.Fatalf("error = %q, want %q", resp.Error, want)
	}
}

func TestBusAuditIsNotACredentialPushMethod(t *testing.T) {
	d := newHandlerDaemon(t)
	params, _ := json.Marshal(map[string]uint64{"seq": 1})
	resp := d.dispatchAs(daemonrpc.Request{Method: "bus.audit", Params: params}, caller{scope: ScopeCredentialPush, fingerprint: "SHA256:x"})
	if resp.Error == "" || !strings.Contains(resp.Error, "not permitted") {
		t.Fatalf("credential-push key reached bus.audit: %+v", resp)
	}
}

func TestBusAuditAnswersWhileTheDaemonAdopts(t *testing.T) {
	if !startingAllows(daemonrpc.Request{Method: "bus.audit"}) {
		t.Error("bus.audit is refused while the daemon adopts; it reads the broker, not tmux")
	}
	if credentialPushMethods["bus.audit"] || localOnlyMethods["bus.audit"] {
		t.Error("bus.audit is in credentialPushMethods or localOnlyMethods")
	}
}

func TestBusAuditCreatesNoConsumer(t *testing.T) {
	d := newHandlerDaemon(t)
	startTestLeafBroker(t, d, "", false)
	js, st := auditStream(t, d)
	seq := publishAudit(t, js, nil, "r")
	count := func() int {
		info, err := st.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return info.State.Consumers
	}
	before := count()
	if resp := auditCall(d, seq); resp.Error != "" {
		t.Fatal(resp.Error)
	}
	if after := count(); after != before {
		t.Errorf("consumers %d before the read, %d after", before, after)
	}
}
