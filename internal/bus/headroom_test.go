package bus

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/arcavenae/marvel/internal/events"
)

const (
	kib = uint64(1) << 10
	gib = uint64(1) << 30
)

func TestHeadroomLowThresholds(t *testing.T) {
	t.Parallel()
	roomy := Headroom{FSFree: 200 * gib, FSTotal: 1000 * gib}
	cases := []struct {
		name string
		h    Headroom
		want string // the names Problems reports; "" is no warning
	}{
		{"nothing known", Headroom{}, ""},
		{"file store at 79 percent", Headroom{FileUsed: 79, FileLimit: 100, FSFree: roomy.FSFree, FSTotal: roomy.FSTotal}, ""},
		{"file store at 80 percent", Headroom{FileUsed: 80, FileLimit: 100, FSFree: roomy.FSFree, FSTotal: roomy.FSTotal}, "file store"},
		{"file store over its cap", Headroom{FileUsed: 120, FileLimit: 100, FSFree: roomy.FSFree, FSTotal: roomy.FSTotal}, "file store"},
		{"no file cap reported", Headroom{FileUsed: 90, FileLimit: 0, FSFree: roomy.FSFree, FSTotal: roomy.FSTotal}, ""},
		{"memory store at 85 percent", Headroom{MemUsed: 85, MemLimit: 100, FSFree: roomy.FSFree, FSTotal: roomy.FSTotal}, "memory store"},
		{"both stores", Headroom{FileUsed: 90, FileLimit: 100, MemUsed: 90, MemLimit: 100, FSFree: roomy.FSFree, FSTotal: roomy.FSTotal}, "file store, memory store"},
		{"filesystem 19 percent free", Headroom{FSFree: 190 * gib, FSTotal: 1000 * gib}, ""},
		{"filesystem 9 percent free", Headroom{FSFree: 90 * gib, FSTotal: 1000 * gib}, "store filesystem"},
		{"filesystem under 1 GiB free, however large", Headroom{FSFree: gib - 1, FSTotal: 1000 * gib}, "store filesystem"},
		{"small filesystem with 1 GiB and 20 percent free", Headroom{FSFree: 2 * gib, FSTotal: 10 * gib}, ""},
		{"small filesystem under 1 GiB free", Headroom{FSFree: gib / 2, FSTotal: gib}, "store filesystem"},
		{"filesystem size unknown", Headroom{FSFree: 5, FSTotal: 0}, ""},
	}
	for _, tc := range cases {
		if got := tc.h.Problems(); got != tc.want {
			t.Errorf("%s: Problems() = %q, want %q", tc.name, got, tc.want)
		}
		if (len(tc.h.Low()) > 0) != (tc.want != "") {
			t.Errorf("%s: Low() = %q does not match Problems() %q", tc.name, tc.h.Low(), tc.want)
		}
	}
	// A warning carries the numbers an operator needs.
	w := Headroom{FileUsed: 90, FileLimit: 100}.Low()
	if len(w) != 1 || !strings.Contains(w[0], "90") || !strings.Contains(w[0], "100") {
		t.Errorf("warning %q does not state used and limit", w)
	}
}

// Against a real broker: the cap and the use come from /jsz, and the
// filesystem reading comes from the store directory.
func TestReadHeadroomReadsTheBrokersCapAndUse(t *testing.T) {
	const cap = 1 << 20
	url, mon := startScratchBrokerMon(t, cap)
	ctx := context.Background()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, _ := jetstream.New(nc)
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "FILL", Subjects: []string{"fill.>"}, Storage: jetstream.FileStorage}); err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 60*1024)
	for i := 0; i < 14; i++ { // about 840 KiB of a 1 MiB cap
		if _, err := js.Publish(ctx, "fill.x", chunk); err != nil {
			t.Fatalf("fill %d: %v", i, err)
		}
	}
	h, err := ReadHeadroom(ctx, mon, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if h.FileLimit != cap {
		t.Errorf("file limit = %d, want %d", h.FileLimit, cap)
	}
	if h.FileUsed < 800*kib || h.FileUsed > cap {
		t.Errorf("file used = %d, want about 840 KiB", h.FileUsed)
	}
	if h.FSTotal == 0 || h.FSFree == 0 || h.FSFree > h.FSTotal {
		t.Errorf("filesystem reading = free %d of %d", h.FSFree, h.FSTotal)
	}
	if got := h.Problems(); !strings.Contains(got, "file store") {
		t.Errorf("a store at 80 percent of its cap reads %q", got)
	}
}

// No monitor to ask is no information.
func TestReadHeadroomWithNoBrokerIsAnError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if h, err := ReadHeadroom(ctx, "127.0.0.1:"+strconv.Itoa(freePort(t)), t.TempDir()); err == nil {
		t.Fatalf("headroom against nothing returned a reading: %+v", h)
	}
}

func storeLowEvents(ring *events.Ring) []events.Event {
	return ring.Snapshot(events.Filter{Kind: events.KindBusStoreLow}, 0)
}

// The supervisor warns once per change in the low set, shows it in status,
// keeps the last reading through an error, and neither holds spawns nor
// restarts the broker.
func TestHeadroomLowWarnsOncePerChangeAndNeverHoldsOrRestarts(t *testing.T) {
	s, _, ring := newTestSupervisor(t, "")
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	pid := s.Status().PID
	var mu sync.Mutex
	reading := Headroom{}
	var readErr error
	s.mu.Lock()
	s.headroomFn = func(context.Context) (Headroom, error) {
		mu.Lock()
		defer mu.Unlock()
		return reading, readErr
	}
	s.mu.Unlock()
	set := func(h Headroom, err error) {
		mu.Lock()
		reading, readErr = h, err
		mu.Unlock()
	}

	set(Headroom{FileUsed: 90, FileLimit: 100}, nil)
	s.checkStructure(context.Background())
	s.checkStructure(context.Background())
	evs := storeLowEvents(ring)
	if len(evs) != 1 || evs[0].Severity != events.SeverityWarning || !strings.Contains(evs[0].Message, "file store") {
		t.Fatalf("bus.store.low events = %+v, want exactly one warning naming the file store", evs)
	}
	if !s.Ready() {
		t.Error("a headroom warning held new spawns")
	}
	if st := s.Status(); st.HeadroomLow != "file store" || st.PID != pid || st.Restarts != 0 {
		t.Fatalf("status = %+v, want headroom_low file store and no restart", st)
	}

	set(Headroom{}, errors.New("monitor unreachable"))
	s.checkStructure(context.Background())
	if len(storeLowEvents(ring)) != 1 || s.Status().HeadroomLow != "file store" {
		t.Fatal("a read error changed the reading")
	}

	set(Headroom{FileUsed: 90, FileLimit: 100, MemUsed: 95, MemLimit: 100}, nil)
	s.checkStructure(context.Background())
	if n := len(storeLowEvents(ring)); n != 2 {
		t.Fatalf("events after a wider low set = %d, want 2", n)
	}

	set(Headroom{}, nil)
	s.checkStructure(context.Background())
	if n := len(storeLowEvents(ring)); n != 2 {
		t.Errorf("events after clearing = %d, want still 2", n)
	}
	if st := s.Status(); st.HeadroomLow != "" || st.Restarts != 0 {
		t.Errorf("status after clearing = %+v", st)
	}
}
