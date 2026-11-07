package daemon

import (
	"bytes"
	"encoding/json"
	"slices"
	"sort"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/register"
)

func describeSessionRaw(t *testing.T, d *Daemon, key string) json.RawMessage {
	t.Helper()
	resp := d.handleDescribe(mustMarshal(t, map[string]string{"resource_type": "session", "name": key}))
	if resp.Error != "" {
		t.Fatalf("describe session %s: %s", key, resp.Error)
	}
	return resp.Result
}

func sortedKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("not an object: %v", err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// describe session carries a register block for a stored seat, and the block
// is the row get register serves for that seat, byte for byte.
func TestDescribeSessionCarriesTheRegisterBlock(t *testing.T) {
	d := newHandlerDaemon(t)
	sessions := registerFixture(t, d)
	rows, _ := getRegister(t, d)
	bySeat := map[string]register.Row{}
	for _, r := range rows {
		bySeat[r.Seat] = r
	}
	for _, s := range sessions {
		raw := describeSessionRaw(t, d, s.Key())
		var top map[string]json.RawMessage
		if err := json.Unmarshal(raw, &top); err != nil {
			t.Fatal(err)
		}
		block, ok := top["register"]
		if !ok {
			t.Fatalf("%s: describe session has no register key; keys %v", s.Key(), sortedKeys(t, raw))
		}
		want, err := json.Marshal(bySeat[s.Key()])
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(block, want) {
			t.Errorf("%s: register block differs from the get register row:\nblock %s\nrow   %s", s.Key(), block, want)
		}
	}
}

// The block is one new key. Every key the stored session has stays in the
// describe output (describe also stamps a few read-time keys of its own, so the
// stored keys are a floor and not the whole set), the register key appears
// once, and the session decodes as before.
func TestDescribeSessionKeepsItsExistingKeys(t *testing.T) {
	d := newHandlerDaemon(t)
	sessions := registerFixture(t, d)
	for _, s := range sessions {
		raw := describeSessionRaw(t, d, s.Key())
		stored, err := d.store.GetSession(s.Key())
		if err != nil {
			t.Fatal(err)
		}
		storedJSON, err := json.Marshal(stored)
		if err != nil {
			t.Fatal(err)
		}
		got := sortedKeys(t, raw)
		for _, k := range sortedKeys(t, storedJSON) {
			if !slices.Contains(got, k) {
				t.Errorf("%s: describe session lost the key %q", s.Key(), k)
			}
		}
		if n := countOf(got, "register"); n != 1 {
			t.Errorf("%s: register appears %d times in %v, want once", s.Key(), n, got)
		}
		var decoded api.Session
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Key() != s.Key() || decoded.Role != s.Role || decoded.Team != s.Team {
			t.Errorf("%s: decoded as %s role %s team %s", s.Key(), decoded.Key(), decoded.Role, decoded.Team)
		}
	}
}

func countOf(keys []string, k string) int {
	n := 0
	for _, x := range keys {
		if x == k {
			n++
		}
	}
	return n
}

// A seat whose team is gone still gets a block, with the two base duties.
func TestDescribeSessionRegisterBlockForASeatWhoseTeamNoLongerResolves(t *testing.T) {
	d := newHandlerDaemon(t)
	orphan := &api.Session{Name: "gone-worker-g1-0", Workspace: "regws", Team: "gone", Role: "worker", Generation: 1, State: api.SessionRunning}
	if err := d.store.CreateSession(orphan); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Register register.Row `json:"register"`
	}
	if err := json.Unmarshal(describeSessionRaw(t, d, orphan.Key()), &out); err != nil {
		t.Fatal(err)
	}
	if out.Register.Seat != orphan.Key() || len(out.Register.Duties) != 2 ||
		out.Register.Duties["duty.first-turn"].Source == "" || out.Register.Duties["duty.ack"].Source == "" {
		t.Fatalf("block = %+v, want the seat and exactly first-turn and ack", out.Register)
	}
}

func TestDescribeSessionRegisterBlockMatchesTheRowSchema(t *testing.T) {
	d := newHandlerDaemon(t)
	sessions := registerFixture(t, d)
	sch := compileRegisterSchema(t)
	for _, s := range sessions {
		var top map[string]json.RawMessage
		if err := json.Unmarshal(describeSessionRaw(t, d, s.Key()), &top); err != nil {
			t.Fatal(err)
		}
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(top["register"]))
		if err != nil {
			t.Fatal(err)
		}
		if err := sch.Validate(inst); err != nil {
			t.Errorf("%s: block does not match the schema: %v", s.Key(), err)
		}
	}
}

func TestDescribeUnknownSessionStillErrors(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := d.handleDescribe(mustMarshal(t, map[string]string{"resource_type": "session", "name": "regws/nope"}))
	if resp.Error == "" {
		t.Fatal("describing a session that does not exist succeeded")
	}
}
