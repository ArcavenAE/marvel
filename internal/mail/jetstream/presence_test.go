package jetstream

import (
	"context"
	"strings"
	"testing"

	njs "github.com/nats-io/nats.go/jetstream"
)

// Liveness is not a consumer property: a durable is live while its session has
// a presence key. The local tier reads AGENT_STATE keys
// presence.<team>.<id>.<instance> (director unread.go, quietKeys, at director
// fedbb54); the durable mcp_<id>_<instance> is live when <id>.<instance> has a key.
func TestLocalDurableIsLiveExactlyWhileItsPresenceKeyExists(t *testing.T) {
	b := newScratchBroker(t)
	b.publish(subjA)
	b.consumer("mcp_alpha_i1", subjA)    // key present
	b.consumer("mcp_alpha_i2", subjA)    // never had a key
	b.consumer("mcp_alpha_i3", subjA)    // key deleted
	b.consumer("mcp_beta_i1", subjOther) // same instance as alpha_i1, other seat: no key
	b.present("AGENT_STATE", "presence.squad.alpha.i1", `{"state":"idle"}`)
	b.present("AGENT_STATE", "presence.squad.alpha.i3", `{"state":"idle"}`)
	b.gone("AGENT_STATE", "presence.squad.alpha.i3")

	got, err := NewQueue(b.js, LocalPresence).Durables(context.Background(), testStream)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{
		"mcp_alpha_i1": true, "mcp_alpha_i2": false, "mcp_alpha_i3": false, "mcp_beta_i1": false,
	} {
		if d := durableByName(t, got, name); d.Live != want {
			t.Errorf("%s: Live = %v, want %v", name, d.Live, want)
		}
	}
}

// The id is split from the instance at the last underscore, so an id that
// carries underscores still matches its key.
func TestLiveSplitsTheDurableAtTheLastUnderscore(t *testing.T) {
	b := newScratchBroker(t)
	b.publish(subjA)
	b.consumer("mcp_alpha_one_i9", subjA)
	b.present("AGENT_STATE", "presence.squad.alpha_one.i9", `{}`)

	got, err := NewQueue(b.js, LocalPresence).Durables(context.Background(), testStream)
	if err != nil {
		t.Fatal(err)
	}
	if d := durableByName(t, got, "mcp_alpha_one_i9"); !d.Live {
		t.Error("a durable whose agent id holds an underscore was read as departed")
	}
}

// The global tier names a session by the key's JSON value, agent_id and
// instance, never by parsing the key; its durables are mcp_global_<id>_<instance>.
func TestGlobalDurableIsLiveByTheValuesAgentIDAndInstance(t *testing.T) {
	b := newScratchBroker(t)
	if _, err := b.js.CreateStream(context.Background(), njs.StreamConfig{Name: "GLOBAL_TO_DIRECTOR", Subjects: []string{"global.director.>"}}); err != nil {
		t.Fatal(err)
	}
	mk := func(name string) {
		if _, err := b.js.CreateOrUpdateConsumer(context.Background(), "GLOBAL_TO_DIRECTOR", njs.ConsumerConfig{
			Durable: name, AckPolicy: njs.AckExplicitPolicy, FilterSubject: "global.director.inbox",
		}); err != nil {
			t.Fatal(err)
		}
	}
	mk("mcp_global_director_I9") // live: the value says so, whatever the key looks like
	mk("mcp_global_director_I8") // the key names I8 but the value says otherwise
	b.present("GLOBAL_PRESENCE", "presence.director.opaque-key-1", `{"agent_id":"director","instance":"I9","cluster":"c"}`)
	b.present("GLOBAL_PRESENCE", "presence.director.I8", `{"agent_id":"someone-else","instance":"I8"}`)

	got, err := NewQueue(b.js, GlobalPresence).Durables(context.Background(), "GLOBAL_TO_DIRECTOR")
	if err != nil {
		t.Fatal(err)
	}
	if d := durableByName(t, got, "mcp_global_director_I9"); !d.Live {
		t.Error("the durable named by the value's agent_id and instance was read as departed")
	}
	if d := durableByName(t, got, "mcp_global_director_I8"); d.Live {
		t.Error("a durable was read as live from a key name; the value names someone else")
	}
}

// A presence bucket that cannot be read makes the tier an error, so the seat
// reads unknown, never "every durable departed".
func TestAnUnreadablePresenceBucketIsAnErrorNotAllDeparted(t *testing.T) {
	b := newScratchBroker(t)
	b.publish(subjA)
	b.consumer("mcp_alpha_i1", subjA)
	_, err := NewQueue(b.js, Presence{Bucket: "NO_SUCH_BUCKET"}).Durables(context.Background(), testStream)
	if err == nil {
		t.Fatal("Durables returned no error with its presence bucket missing")
	}
	if !strings.Contains(err.Error(), "NO_SUCH_BUCKET") {
		t.Errorf("error %q does not name the bucket", err)
	}
}

// Reading presence creates no consumer: a watcher on the bucket would be an
// ephemeral consumer, which the reader must never leave on a fleet broker.
func TestReadingPresenceCreatesNoConsumer(t *testing.T) {
	b := newScratchBroker(t)
	b.publish(subjA)
	b.consumer("mcp_alpha_i1", subjA)
	b.present("AGENT_STATE", "presence.squad.alpha.i1", `{}`)
	before := b.consumerCount("KV_AGENT_STATE")
	if _, err := NewQueue(b.js, LocalPresence).Durables(context.Background(), testStream); err != nil {
		t.Fatal(err)
	}
	if after := b.consumerCount("KV_AGENT_STATE"); after != before {
		t.Errorf("KV_AGENT_STATE consumers %d before, %d after a read", before, after)
	}
}
