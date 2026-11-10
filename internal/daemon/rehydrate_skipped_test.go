package daemon

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// A stored row that the name rule leaves out at start is announced on the
// ring, one event per row, so an operator sees it without reading the log.
func TestDaemonStartAnnouncesEachRowTheNameRuleLeftOut(t *testing.T) {
	skipIfNoTmux(t)
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s := api.NewStore()
	if err := s.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateWorkspace(&api.Workspace{Name: "ws"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseBolt(); err != nil {
		t.Fatal(err)
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, tm := range []api.Team{{Name: "team_b", Workspace: "ws"}, {Name: "team c", Workspace: "ws"}} {
			b, merr := json.Marshal(tm)
			if merr != nil {
				return merr
			}
			if perr := tx.Bucket([]byte("teams")).Put([]byte(tm.Key()), b); perr != nil {
				return perr
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	ring := events.NewRing(events.DefaultCapacity)
	if _, err := NewWithOptions(Options{StateBolt: path, Events: ring}); err != nil {
		t.Fatalf("start: %v", err)
	}
	got := ring.Snapshot(events.Filter{Kind: events.KindStoreRowSkipped}, 50)
	if len(got) != 2 {
		t.Fatalf("want one %s event per skipped row, got %d: %+v", events.KindStoreRowSkipped, len(got), got)
	}
	for _, ev := range got {
		if ev.Severity != events.SeverityWarning || !strings.Contains(ev.Message, "teams") {
			t.Fatalf("event %+v: want a warning naming the teams bucket", ev)
		}
	}
}
