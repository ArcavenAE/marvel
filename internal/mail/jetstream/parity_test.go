package jetstream

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	njs "github.com/nats-io/nats.go/jetstream"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/mail"
)

// The parity fixture reads one broker state twice: once by this adapter and once
// by director's reader (`director-mcp unread --json`, director fedbb54,
// cmd/director-mcp/unread.go), and compares them per durable. The golden file is
// director's rows with every time dropped; it is regenerated with
// UPDATE_PARITY_GOLDEN=<path to write> and DIRECTOR_MCP_BIN naming a director-mcp built from
// the commit the golden names.

const parityGolden = "testdata/unread_parity.golden.json"

// parityRow is one durable as both readers state it.
type parityRow struct {
	Durable    string   `json:"durable"`
	Filters    []string `json:"filters"`
	Depth      int64    `json:"depth"` // NumPending + NumAckPending, director's "pending"
	AckPending int64    `json:"ack_pending"`
	AckFloor   uint64   `json:"ack_floor"`
	OldestSeq  uint64   `json:"oldest_seq"`
	Live       bool     `json:"live"`
	// oldest is the oldest unread message's stored time, compared to the second
	// in a live run and kept out of the golden.
	oldest time.Time
}

// buildParityState makes ten cases on the scratch broker, one seat each, so no
// case reads another's mail.
// updateParityGolden and directorMCPBin are the variables the parity tests are
// switched by, as the binary started with them. The test guard removes every
// variable off its list before the tests run, so the tests read them from here,
// from before that, and not from the environment.
var (
	updateParityGolden = os.Getenv("UPDATE_PARITY_GOLDEN")
	directorMCPBin     = os.Getenv("DIRECTOR_MCP_BIN")
)

func buildParityState(t *testing.T, b *scratchBroker) {
	t.Helper()
	subj := func(id string) string { return "agent.ws.squad." + id + ".inbox" }
	role := "agent.ws.squad.role.worker.inbox"

	// 1. one live durable: three messages, two fetched, one acked.
	for i := 0; i < 3; i++ {
		b.publish(subj("c1"))
	}
	b.take(b.consumer("mcp_c1_i1", subj("c1")), 2, 0)
	b.present("AGENT_STATE", "presence.squad.c1.i1", `{"state":"idle"}`)

	// 2. two live durables of one seat, one acked ahead of the other.
	for i := 0; i < 4; i++ {
		b.publish(subj("c2"))
	}
	b.take(b.consumer("mcp_c2_a", subj("c2")), 4, 0, 1, 2)
	b.take(b.consumer("mcp_c2_b", subj("c2")), 4, 0)
	b.present("AGENT_STATE", "presence.squad.c2.a", `{}`)
	b.present("AGENT_STATE", "presence.squad.c2.b", `{}`)

	// 3. a successor resumed past a departed durable that acked up to 3.
	for i := 0; i < 5; i++ {
		b.publish(subj("c3"))
	}
	b.take(b.consumer("mcp_c3_old", subj("c3")), 3, 0, 1, 2)
	if _, err := b.js.CreateOrUpdateConsumer(context.Background(), testStream, njs.ConsumerConfig{
		Durable: "mcp_c3_new", AckPolicy: njs.AckExplicitPolicy, FilterSubject: subj("c3"),
		DeliverPolicy: njs.DeliverByStartSequencePolicy, OptStartSeq: lastSeq(t, b, subj("c3")) - 1,
	}); err != nil {
		t.Fatal(err)
	}
	b.present("AGENT_STATE", "presence.squad.c3.new", `{}`)

	// 4. a seat with no live durable: its only durable departed after one ack.
	for i := 0; i < 3; i++ {
		b.publish(subj("c4"))
	}
	b.take(b.consumer("mcp_c4_gone", subj("c4")), 2, 0)

	// 5. role mail on the same durable, counted once.
	b.publish(subj("c5"))
	b.publish(role)
	b.publish(subj("c5"))
	b.consumer("mcp_c5_i1", subj("c5"), role)
	b.present("AGENT_STATE", "presence.squad.c5.i1", `{}`)

	// 6. an ack-pending message, read in handled mode and not yet acked.
	for i := 0; i < 2; i++ {
		b.publish(subj("c6"))
	}
	b.take(b.consumer("mcp_c6_i1", subj("c6")), 2)
	b.present("AGENT_STATE", "presence.squad.c6.i1", `{}`)

	// 7. an ack out of stream order: the later message acked, the earlier held.
	for i := 0; i < 3; i++ {
		b.publish(subj("c7"))
	}
	b.take(b.consumer("mcp_c7_i1", subj("c7")), 3, 2)
	b.present("AGENT_STATE", "presence.squad.c7.i1", `{}`)

	// 8. a Nak'd redelivery: the first message refused, the second acked.
	for i := 0; i < 2; i++ {
		b.publish(subj("c8"))
	}
	b.takeNak(b.consumer("mcp_c8_i1", subj("c8")), 2, 0, 1)
	b.present("AGENT_STATE", "presence.squad.c8.i1", `{}`)

	// 10. nothing unread: everything acked.
	for i := 0; i < 2; i++ {
		b.publish(subj("c10"))
	}
	b.take(b.consumer("mcp_c10_i1", subj("c10")), 2, 0, 1)
	b.present("AGENT_STATE", "presence.squad.c10.i1", `{}`)

	// 9. the global tier: a live hub durable with one of three acked.
	if _, err := b.js.CreateStream(context.Background(), njs.StreamConfig{Name: "GLOBAL_TO_DIRECTOR", Subjects: []string{"global.director.>"}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := b.js.Publish(context.Background(), "global.director.inbox", []byte("m")); err != nil {
			t.Fatal(err)
		}
	}
	c, err := b.js.CreateOrUpdateConsumer(context.Background(), "GLOBAL_TO_DIRECTOR", njs.ConsumerConfig{
		Durable: "mcp_global_director_g1", AckPolicy: njs.AckExplicitPolicy, FilterSubject: "global.director.inbox",
	})
	if err != nil {
		t.Fatal(err)
	}
	b.take(c, 3, 0)
	b.present("GLOBAL_PRESENCE", "presence.director.g1", `{"agent_id":"director","instance":"g1","state":"idle"}`)
}

// lastSeq is the stream sequence of the newest message on subject.
func lastSeq(t *testing.T, b *scratchBroker, subject string) uint64 {
	t.Helper()
	m, err := b.st.GetLastMsgForSubject(context.Background(), subject)
	if err != nil {
		t.Fatal(err)
	}
	return m.Sequence
}

// adapterRows reads every durable of stream as the adapter states it.
func adapterRows(t *testing.T, b *scratchBroker, stream string, p Presence) []parityRow {
	t.Helper()
	q := NewQueue(b.js, p)
	ds, err := q.Durables(context.Background(), stream)
	if err != nil {
		t.Fatal(err)
	}
	var rows []parityRow
	for _, d := range ds.List {
		r := parityRow{Durable: d.Name, Filters: d.Filters, Depth: d.Pending + d.AckPending, AckPending: d.AckPending, AckFloor: d.AckFloor, Live: d.Live}
		if r.Depth > 0 {
			m, ok, err := q.Oldest(context.Background(), stream, d.AckFloor, d.Filters)
			if err != nil {
				t.Fatal(err)
			}
			if ok {
				r.OldestSeq, r.oldest = m.Seq, m.Stored
			}
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Durable < rows[j].Durable })
	return rows
}

type directorReport struct {
	Durables []struct {
		Durable    string   `json:"durable"`
		Filters    []string `json:"filters"`
		Pending    int64    `json:"pending"`
		AckPending int64    `json:"ack_pending"`
		AckFloor   uint64   `json:"ack_floor"`
		OldestSeq  uint64   `json:"oldest_unread_seq"`
		OldestAt   string   `json:"oldest_unread_at"`
		Live       bool     `json:"live"`
	} `json:"durables"`
}

// directorRows runs director's reader against the broker and returns its rows.
func directorRows(t *testing.T, bin, url string, global bool) []parityRow {
	t.Helper()
	args := []string{"unread", "--json", "--all"}
	if global {
		args = []string{"unread", "--json", "--all", "--global"}
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "NATS_URL="+url)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("director-mcp %s: %v\n%s", strings.Join(args, " "), err, errb.String())
	}
	var reports []directorReport
	if global {
		var g struct {
			Reports []directorReport `json:"reports"`
			Error   string           `json:"error"`
		}
		if err := json.Unmarshal(out.Bytes(), &g); err != nil || g.Error != "" {
			t.Fatalf("director global report: %v %q\n%s", err, g.Error, out.String())
		}
		reports = g.Reports
	} else {
		var r directorReport
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatalf("director report: %v\n%s", err, out.String())
		}
		reports = []directorReport{r}
	}
	var rows []parityRow
	for _, r := range reports {
		for _, d := range r.Durables {
			row := parityRow{Durable: d.Durable, Filters: d.Filters, Depth: d.Pending, AckPending: d.AckPending, AckFloor: d.AckFloor, OldestSeq: d.OldestSeq, Live: d.Live}
			if d.OldestAt != "" {
				row.oldest, _ = time.Parse(time.RFC3339, d.OldestAt)
			}
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Durable < rows[j].Durable })
	return rows
}

func sameRows(t *testing.T, what string, got, want []parityRow) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d durables, want %d\n got %+v\nwant %+v", what, len(got), len(want), got, want)
	}
	for i := range got {
		g, w := got[i], want[i]
		g.oldest, w.oldest = time.Time{}, time.Time{}
		gj, _ := json.Marshal(g)
		wj, _ := json.Marshal(w)
		if !bytes.Equal(gj, wj) {
			t.Errorf("%s: %s\n got %s\nwant %s", what, g.Durable, gj, wj)
		}
	}
}

// All ten cases, local and global: the adapter's rows equal the golden, which is
// director's own reading with its times dropped.
func TestAdapterRowsEqualTheDirectorGolden(t *testing.T) {
	b := newScratchBroker(t)
	buildParityState(t, b)
	got := append(adapterRows(t, b, testStream, LocalPresence), adapterRows(t, b, "GLOBAL_TO_DIRECTOR", GlobalPresence)...)
	sort.Slice(got, func(i, j int) bool { return got[i].Durable < got[j].Durable })

	if out := updateParityGolden; out != "" {
		bin := directorMCPBin
		if bin == "" {
			t.Fatal("UPDATE_PARITY_GOLDEN needs DIRECTOR_MCP_BIN")
		}
		rows := append(directorRows(t, bin, b.url, false), directorRows(t, bin, b.url, true)...)
		sort.Slice(rows, func(i, j int) bool { return rows[i].Durable < rows[j].Durable })
		data, err := json.MarshalIndent(rows, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s from director-mcp; copy it to %s", out, parityGolden)
	}
	data, err := os.ReadFile(parityGolden)
	if err != nil {
		t.Fatal(err)
	}
	var want []parityRow
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	sameRows(t, "adapter against the golden", got, want)
}

// Live: against a director-mcp binary on the same broker, every field matches
// exactly, and the oldest stored time to the second director prints it in. The
// adapter's age is only checked to be a broker-clock age under a minute; it is
// not compared with director's. It runs when DIRECTOR_MCP_BIN names the binary.
func TestAdapterRowsEqualADirectorRunOnTheSameBroker(t *testing.T) {
	bin := directorMCPBin
	if bin == "" {
		t.Skip("DIRECTOR_MCP_BIN not set; the golden test covers the comparison")
	}
	b := newScratchBroker(t)
	buildParityState(t, b)
	for _, global := range []bool{false, true} {
		stream, p := testStream, LocalPresence
		if global {
			stream, p = "GLOBAL_TO_DIRECTOR", GlobalPresence
		}
		got := adapterRows(t, b, stream, p)
		want := directorRows(t, bin, b.url, global)
		var wantTier []parityRow
		for _, w := range want {
			if strings.HasPrefix(w.Durable, "mcp_global_") == global {
				wantTier = append(wantTier, w)
			}
		}
		sameRows(t, "adapter against director", got, wantTier)
		for i := range got {
			if !got[i].oldest.IsZero() && !got[i].oldest.Truncate(time.Second).Equal(wantTier[i].oldest.Truncate(time.Second)) {
				t.Errorf("%s: oldest stored %v, director %v", got[i].Durable, got[i].oldest, wantTier[i].oldest)
			}
		}
	}
}

// The seat's state from SeatMail over the adapter equals the maximum over the
// golden's per-durable rows, for a seat with one live durable, one with two, and
// one whose only durable departed (the inherited floor).
func TestSeatMailOverTheAdapterAgreesWithTheGoldenRows(t *testing.T) {
	b := newScratchBroker(t)
	buildParityState(t, b)
	data, err := os.ReadFile(parityGolden)
	if err != nil {
		t.Fatal(err)
	}
	var golden []parityRow
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	byName := map[string]parityRow{}
	for _, r := range golden {
		byName[r.Durable] = r
	}
	sm := &mail.SeatMail{Queue: NewQueue(b.js, LocalPresence)}
	for _, c := range []struct {
		id        string
		durables  []string
		wantDepth int64
	}{
		{"c1", []string{"mcp_c1_i1"}, 2},
		{"c2", []string{"mcp_c2_a", "mcp_c2_b"}, 3},
		{"c4", []string{"mcp_c4_gone"}, 2},
	} {
		t.Run(c.id, func(t *testing.T) {
			seat := api.Session{Name: c.id, Workspace: "ws", Team: "squad"}
			st, err := sm.Read(context.Background(), seat)
			if err != nil {
				t.Fatal(err)
			}
			var want int64
			for _, n := range c.durables {
				if d := byName[n].Depth; d > want {
					want = d
				}
			}
			if want != c.wantDepth {
				t.Fatalf("setup: golden maximum for %s is %d, case says %d", c.id, want, c.wantDepth)
			}
			if got, ok := st.Depth.Value(); !ok || got != want {
				t.Errorf("%s: depth = %d (value %v), want the golden maximum %d", c.id, got, ok, want)
			}
			if age, ok := st.OldestAge.Value(); !ok || age < 0 || age > time.Minute {
				t.Errorf("%s: oldest age = %v (value %v), want a broker-clock age under a minute", c.id, age, ok)
			}
		})
	}
}
