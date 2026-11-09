package jetstream

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"
	njs "github.com/nats-io/nats.go/jetstream"
)

// leafPublishAllow is the publish allow list of the director host's leaf in the fleet hub's
// template (director probe/nats-global-tier/hub/nats-server.conf, at director
// fedbb54): what the daemon's credential can send over the leaf into the hub's
// domain "global". Its subscribe list is global.director.> and _INBOX.>.
var leafPublishAllow = []string{
	"global.*.supervisor.inbox", "global.director.>",
	"$JS.global.API.CONSUMER.CREATE.GLOBAL_TO_DIRECTOR.>",
	"$JS.global.API.CONSUMER.DURABLE.CREATE.GLOBAL_TO_DIRECTOR.>",
	"$JS.global.API.CONSUMER.INFO.GLOBAL_TO_DIRECTOR.>",
	"$JS.global.API.CONSUMER.MSG.NEXT.GLOBAL_TO_DIRECTOR.>",
	"$JS.global.API.CONSUMER.DELETE.GLOBAL_TO_DIRECTOR.>",
	"$JS.global.API.STREAM.INFO.GLOBAL_TO_DIRECTOR",
	"$JS.global.API.STREAM.MSG.DELETE.GLOBAL_TO_DIRECTOR",
	"$JS.global.API.INFO",
	"$JS.global.API.$KV.GLOBAL_PRESENCE.>",
	"$JS.global.API.STREAM.INFO.KV_GLOBAL_PRESENCE",
	"$JS.global.API.DIRECT.GET.KV_GLOBAL_PRESENCE.>",
	"$JS.global.API.STREAM.MSG.GET.KV_GLOBAL_PRESENCE",
	"$JS.global.API.CONSUMER.CREATE.KV_GLOBAL_PRESENCE.>",
	"$JS.global.API.CONSUMER.INFO.KV_GLOBAL_PRESENCE.>",
	"$JS.global.API.CONSUMER.DELETE.KV_GLOBAL_PRESENCE.>",
	"$JS.ACK.>",
}

// leafBroker is a scratch broker with an admin user to set the state up and a
// "leaf" user holding exactly the given publish grants, and the adapter's handle
// for the global tier, which reads in the hub's domain.
func leafBroker(t *testing.T, extra ...string) njs.JetStream {
	t.Helper()
	allow := append(append([]string{}, leafPublishAllow...), extra...)
	quoted := make([]string, len(allow))
	for i, a := range allow {
		// A single server rewrites the domain prefix before it checks a publish,
		// so the same grant is written here as $JS.API.... On the hub the leaf's
		// grant keeps the $JS.global.API form (the template's).
		quoted[i] = fmt.Sprintf("%q", strings.ReplaceAll(a, "$JS.global.API.", "$JS.API."))
	}
	auth := fmt.Sprintf(`authorization {
  users = [
    { user: admin, password: admin }
    { user: leaf, password: leaf, permissions {
        publish { allow: [ %s ] }
        subscribe { allow: [ "global.director.>", "_INBOX.>" ] }
    } }
  ]
}
`, strings.Join(quoted, ", "))
	b := newScratchBrokerWith(t, auth, []nats.Option{nats.UserInfo("admin", "admin")})
	b.present("GLOBAL_PRESENCE", "presence.director.g1", `{"agent_id":"director","instance":"g1"}`)
	if _, err := b.js.CreateStream(context.Background(), njs.StreamConfig{Name: "GLOBAL_TO_DIRECTOR", Subjects: []string{"global.director.>"}}); err != nil {
		t.Fatal(err)
	}
	b.publishGlobal("global.director.inbox")
	if _, err := b.js.CreateOrUpdateConsumer(context.Background(), "GLOBAL_TO_DIRECTOR", njs.ConsumerConfig{
		Durable: "mcp_global_director_g1", AckPolicy: njs.AckExplicitPolicy, FilterSubject: "global.director.inbox",
	}); err != nil {
		t.Fatal(err)
	}
	nc, err := nats.Connect(b.url, nats.UserInfo("leaf", "leaf"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	gjs, err := njs.NewWithDomain(nc, "global")
	if err != nil {
		t.Fatal(err)
	}
	return gjs
}

func (b *scratchBroker) publishGlobal(subject string) {
	b.t.Helper()
	if _, err := b.js.Publish(context.Background(), subject, []byte("m")); err != nil {
		b.t.Fatal(err)
	}
}

// With the director host's leaf permissions as the hub's template gives them, the daemon's
// credential cannot list the consumers of GLOBAL_TO_DIRECTOR (the template grants
// CONSUMER.INFO but not CONSUMER.LIST), so the global tier reads unknown, never a
// guess. This is the grant the fleet needs and that this adapter does not make.
func TestTheGlobalTierReadsUnknownUntilTheLeafIsGrantedConsumerList(t *testing.T) {
	gjs := leafBroker(t)
	_, err := NewQueue(gjs, GlobalPresence).Durables(context.Background(), "GLOBAL_TO_DIRECTOR")
	if err == nil {
		t.Fatal("the global tier was read with the template's leaf permissions; the template does grant CONSUMER.LIST now, so the PR text naming this grant is out of date")
	}
}

// Granting CONSUMER.LIST makes the durables readable, and then the oldest message
// needs STREAM.MSG.GET on the stream (the template grants MSG.GET only on the
// presence bucket), which is the second grant.
func TestTheGlobalTierNeedsConsumerListAndStreamMsgGet(t *testing.T) {
	gjs := leafBroker(t, "$JS.global.API.CONSUMER.LIST.GLOBAL_TO_DIRECTOR")
	q := NewQueue(gjs, GlobalPresence)
	ds, err := q.Durables(context.Background(), "GLOBAL_TO_DIRECTOR")
	if err != nil {
		t.Fatalf("with CONSUMER.LIST granted, Durables still failed: %v", err)
	}
	if len(ds.List) != 1 || !ds.List[0].Live {
		t.Fatalf("durables = %+v, want the one live durable (presence is readable under the template)", ds.List)
	}
	if _, _, err := q.Oldest(context.Background(), "GLOBAL_TO_DIRECTOR", 0, ds.List[0].Filters); err == nil {
		t.Fatal("Oldest succeeded without STREAM.MSG.GET on the stream; the template grants it now")
	}
}

// With both grants the whole global read works under a leaf's permissions.
func TestTheGlobalTierReadsWithBothGrants(t *testing.T) {
	gjs := leafBroker(t,
		"$JS.global.API.CONSUMER.LIST.GLOBAL_TO_DIRECTOR",
		"$JS.global.API.STREAM.MSG.GET.GLOBAL_TO_DIRECTOR",
	)
	q := NewQueue(gjs, GlobalPresence)
	ds, err := q.Durables(context.Background(), "GLOBAL_TO_DIRECTOR")
	if err != nil {
		t.Fatal(err)
	}
	m, ok, err := q.Oldest(context.Background(), "GLOBAL_TO_DIRECTOR", ds.List[0].AckFloor, ds.List[0].Filters)
	if err != nil || !ok || m.Seq != 1 {
		t.Fatalf("Oldest = (%+v, %v, %v), want sequence 1", m, ok, err)
	}
	if n, err := q.Count(context.Background(), "GLOBAL_TO_DIRECTOR", ds.List[0].Filters); err != nil || n != 1 {
		t.Errorf("Count = %d, %v, want 1", n, err)
	}
}
