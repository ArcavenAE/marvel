package readiness

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

// A file-shape and consistency check of the shared marrying vectors, and
// nothing more. marvel holds no marrying or routing logic and runs no vector
// through any: director's package does, against a byte copy of this file. This
// test keeps the file itself well formed, so that copy is a copy of a sound
// file (aae-orc-crhs6).

type vectorFile struct {
	Version int      `json:"version"`
	Design  string   `json:"design"`
	Note    string   `json:"note"`
	Rows    []string `json:"rows"`
	Vectors []vector `json:"vectors"`
}

type vector struct {
	Name  string            `json:"name"`
	Row   string            `json:"row"`
	Steps []json.RawMessage `json:"steps"`
}

// step is read loosely: every op has its own shape and the check below reads
// only the seats and what a marry step wants.
type step struct {
	Op    string          `json:"op"`
	Input json.RawMessage `json:"input"`
	Want  json.RawMessage `json:"want"`
	Error string          `json:"error"`
	Lease json.RawMessage `json:"lease"`
}

type marryInput struct {
	Seats []struct {
		Address string `json:"address"`
		Cluster string `json:"cluster"`
	} `json:"seats"`
	Ledger []json.RawMessage `json:"ledger"`
}

type marryWant struct {
	Pairs  []json.RawMessage `json:"pairs"`
	Leases []json.RawMessage `json:"leases"`
}

// pairRow and leaseRow are decoded strictly: an unknown field is an error, so a
// field added to the file on one side is noticed on the other.
type pairRow struct {
	Item    string `json:"item"`
	Seat    string `json:"seat"`
	Cluster string `json:"cluster"`
	Lease   string `json:"lease"`
}

type leaseRow struct {
	ID         string `json:"id"`
	Item       string `json:"item"`
	Seat       string `json:"seat"`
	Cluster    string `json:"cluster"`
	ValidUntil string `json:"valid_until"`
	State      string `json:"state"`
}

func strictInto(t *testing.T, raw []byte, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
}

func loadVectors(t *testing.T) vectorFile {
	t.Helper()
	b, err := os.ReadFile("marry-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var f vectorFile
	strictInto(t, b, &f)
	if len(f.Vectors) == 0 {
		t.Fatal("the vector file holds no vectors")
	}
	return f
}

// clusterOf maps each seat address of a marry input to its cluster. An address
// listed on two clusters maps to "" so a lookup by address alone reads nothing.
func clusterOf(in marryInput) map[string]string {
	m := map[string]string{}
	for _, s := range in.Seats {
		if _, dup := m[s.Address]; dup {
			m[s.Address] = ""
			continue
		}
		m[s.Address] = s.Cluster
	}
	return m
}

// Every pair and every lease the file wants names the cluster of its seat, and
// where the seat's address is on one cluster only the name matches that seat.
func TestMarryVectorsCarryTheClusterOnPairsAndLeases(t *testing.T) {
	f := loadVectors(t)
	for _, v := range f.Vectors {
		for i, raw := range v.Steps {
			var s step
			if err := json.Unmarshal(raw, &s); err != nil {
				t.Fatalf("%s step %d: %v", v.Name, i, err)
			}
			if s.Op != "marry" || len(s.Want) == 0 {
				continue
			}
			var in marryInput
			if err := json.Unmarshal(s.Input, &in); err != nil {
				t.Fatalf("%s step %d input: %v", v.Name, i, err)
			}
			var w marryWant
			if err := json.Unmarshal(s.Want, &w); err != nil {
				t.Fatalf("%s step %d want: %v", v.Name, i, err)
			}
			cl := clusterOf(in)
			for _, p := range w.Pairs {
				var pr pairRow
				strictInto(t, p, &pr)
				checkCluster(t, v.Name, "pair "+pr.Item, pr.Seat, pr.Cluster, cl)
			}
			for _, l := range w.Leases {
				var lr leaseRow
				strictInto(t, l, &lr)
				checkCluster(t, v.Name, "lease "+lr.ID, lr.Seat, lr.Cluster, cl)
			}
		}
	}
}

func checkCluster(t *testing.T, vec, what, seat, cluster string, cl map[string]string) {
	t.Helper()
	if cluster == "" {
		t.Errorf("%s: %s names no cluster", vec, what)
		return
	}
	if want, ok := cl[seat]; ok && want != "" && want != cluster {
		t.Errorf("%s: %s is on cluster %q, its seat %s is on %q", vec, what, cluster, seat, want)
	}
}

// The leases the file carries outside a marry want (the ledger a run reads, and
// the lease a take or a recheck step names) carry the cluster too, since they
// are the same record.
func TestMarryVectorsCarryTheClusterOnEveryLease(t *testing.T) {
	f := loadVectors(t)
	for _, v := range f.Vectors {
		for i, raw := range v.Steps {
			var s step
			if err := json.Unmarshal(raw, &s); err != nil {
				t.Fatalf("%s step %d: %v", v.Name, i, err)
			}
			var in marryInput
			if len(s.Input) > 0 {
				_ = json.Unmarshal(s.Input, &in)
			}
			for _, l := range append(in.Ledger, s.Lease) {
				if len(l) == 0 || string(l) == "null" {
					continue
				}
				var lr leaseRow
				strictInto(t, l, &lr)
				if lr.Cluster == "" {
					t.Errorf("%s step %d: lease %s names no cluster", v.Name, i, lr.ID)
				}
			}
		}
	}
}

// One seat name on two clusters stays two seats: the file holds a vector with
// the same address on two clusters whose want pairs each with its own.
func TestMarryVectorsHoldOneNameOnTwoClusters(t *testing.T) {
	f := loadVectors(t)
	for _, v := range f.Vectors {
		if v.Row != "3.1 seat key" {
			continue
		}
		for _, raw := range v.Steps {
			var s step
			if err := json.Unmarshal(raw, &s); err != nil {
				t.Fatal(err)
			}
			if s.Op != "marry" || len(s.Want) == 0 {
				continue
			}
			var in marryInput
			if err := json.Unmarshal(s.Input, &in); err != nil {
				t.Fatal(err)
			}
			var w marryWant
			if err := json.Unmarshal(s.Want, &w); err != nil {
				t.Fatal(err)
			}
			addrClusters := map[string]map[string]bool{}
			for _, st := range in.Seats {
				if addrClusters[st.Address] == nil {
					addrClusters[st.Address] = map[string]bool{}
				}
				addrClusters[st.Address][st.Cluster] = true
			}
			shared := false
			for _, c := range addrClusters {
				shared = shared || len(c) > 1
			}
			if !shared {
				continue
			}
			got := map[string]bool{}
			for _, p := range w.Pairs {
				var pr pairRow
				strictInto(t, p, &pr)
				got[pr.Cluster] = true
			}
			if len(w.Pairs) != 2 || len(got) != 2 {
				t.Fatalf("%s: want two pairs on two clusters, got %d pairs on %d clusters", v.Name, len(w.Pairs), len(got))
			}
			return
		}
	}
	t.Fatal("the file holds no vector for one seat name on two clusters")
}
