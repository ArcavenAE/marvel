package daemon

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/register"
)

// registerFixture stores one team with two roles and three live sessions: two
// of the worker role, which declares a duty, and one of the plain role.
func registerFixture(t *testing.T, d *Daemon) []*api.Session {
	t.Helper()
	team := &api.Team{
		Name: "squad", Workspace: "regws",
		Roles: []api.Role{
			{Name: "worker", Replicas: 2, Duties: []string{api.DutyGHRead}},
			{Name: "plain", Replicas: 1},
		},
	}
	if err := d.store.CreateTeam(team); err != nil {
		t.Fatalf("create team: %v", err)
	}
	var out []*api.Session
	for _, s := range []*api.Session{
		{Name: "squad-worker-g1-1", Workspace: "regws", Team: "squad", Role: "worker", Generation: 1, State: api.SessionRunning},
		{Name: "squad-plain-g1-0", Workspace: "regws", Team: "squad", Role: "plain", Generation: 1, State: api.SessionRunning},
		{Name: "squad-worker-g1-0", Workspace: "regws", Team: "squad", Role: "worker", Generation: 1, State: api.SessionRunning},
	} {
		if err := d.store.CreateSession(s); err != nil {
			t.Fatalf("create %s: %v", s.Name, err)
		}
		out = append(out, s)
	}
	return out
}

func getRegister(t *testing.T, d *Daemon) ([]register.Row, json.RawMessage) {
	t.Helper()
	resp := d.handleGet(mustMarshal(t, map[string]string{"resource_type": "register"}))
	if resp.Error != "" {
		t.Fatalf("get register: %s", resp.Error)
	}
	var rows []register.Row
	if err := json.Unmarshal(resp.Result, &rows); err != nil {
		t.Fatalf("decode register rows: %v", err)
	}
	return rows, resp.Result
}

// The acceptance test: the RPC returns the rows the builder makes, one for each
// live session, ordered by seat key.
func TestGetRegisterReturnsRowsBuiltByTheBuilder(t *testing.T) {
	d := newHandlerDaemon(t)
	sessions := registerFixture(t, d)

	var want []register.Row
	for _, s := range sessions {
		role := api.Role{Name: s.Role}
		if s.Role == "worker" {
			role.Duties = []string{api.DutyGHRead}
		}
		want = append(want, register.Build(register.Input{Seat: s.Key(), Duties: config.ResolvedDuties(role)}))
	}
	sort.Slice(want, func(i, j int) bool { return want[i].Seat < want[j].Seat })

	got, _ := getRegister(t, d)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("get register = %+v\nwant the builder's rows %+v", got, want)
	}
}

func TestGetRegisterRowsCarryTheResolvedDuties(t *testing.T) {
	d := newHandlerDaemon(t)
	registerFixture(t, d)
	got, _ := getRegister(t, d)
	for _, r := range got {
		_, hasFirst := r.Duties["duty.first-turn"]
		_, hasAck := r.Duties["duty.ack"]
		_, hasRead := r.Duties["duty.gh.read"]
		wantRead := r.Seat != "regws/squad-plain-g1-0"
		if !hasFirst || !hasAck || hasRead != wantRead {
			t.Errorf("%s duties = %v, want first-turn and ack, and gh.read only for a worker", r.Seat, r.Duties)
		}
	}
}

// A seat whose team is gone from the store still gets a row with the two duties
// every seat carries, and the request does not fail.
func TestGetRegisterRowForASeatWhoseTeamNoLongerResolves(t *testing.T) {
	d := newHandlerDaemon(t)
	orphan := &api.Session{Name: "gone-worker-g1-0", Workspace: "regws", Team: "gone", Role: "worker", Generation: 1, State: api.SessionRunning}
	if err := d.store.CreateSession(orphan); err != nil {
		t.Fatal(err)
	}
	got, _ := getRegister(t, d)
	if len(got) != 1 || got[0].Seat != orphan.Key() {
		t.Fatalf("rows = %+v, want one for %s", got, orphan.Key())
	}
	if len(got[0].Duties) != 2 || got[0].Duties["duty.first-turn"].Source == "" || got[0].Duties["duty.ack"].Source == "" {
		t.Fatalf("duties = %v, want exactly first-turn and ack", got[0].Duties)
	}
}

// The register lists the seats that exist: the same keys as the store's live
// sessions, and no row for the synthetic rows get sessions adds for a role that
// is down in its backoff window.
func TestGetRegisterListsLiveSessionsOnly(t *testing.T) {
	d := newHandlerDaemon(t)
	registerFixture(t, d)
	var want []string
	for _, s := range d.store.ListSessions() {
		want = append(want, s.Key())
	}
	sort.Strings(want)
	got, _ := getRegister(t, d)
	var seats []string
	for _, r := range got {
		seats = append(seats, r.Seat)
	}
	if !reflect.DeepEqual(seats, want) {
		t.Fatalf("register seats = %v, want the live sessions %v", seats, want)
	}
}

// Reading the register leaves get sessions as it was: nothing the register
// does stamps or edits the sessions it describes.
func TestGetRegisterLeavesGetSessionsUnchanged(t *testing.T) {
	d := newHandlerDaemon(t)
	registerFixture(t, d)
	// get sessions does not sort, so the rows are compared by key.
	read := func() []byte {
		resp := d.handleGet(mustMarshal(t, map[string]string{"resource_type": "sessions"}))
		if resp.Error != "" {
			t.Fatalf("get sessions: %s", resp.Error)
		}
		var rows []api.Session
		if err := json.Unmarshal(resp.Result, &rows); err != nil {
			t.Fatal(err)
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].Key() < rows[j].Key() })
		out, err := json.Marshal(rows)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := read()
	getRegister(t, d)
	if after := read(); !bytes.Equal(before, after) {
		t.Fatalf("get sessions changed after get register:\nbefore %s\nafter  %s", before, after)
	}
}

// The wire shape is the schema's: every row the RPC returns validates.
func TestGetRegisterRowsMatchTheRowSchema(t *testing.T) {
	d := newHandlerDaemon(t)
	registerFixture(t, d)
	_, raw := getRegister(t, d)

	sch := compileRegisterSchema(t)
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) == 0 {
		t.Fatalf("result is not a non-empty array: %v", err)
	}
	for _, row := range rows {
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(row))
		if err != nil {
			t.Fatal(err)
		}
		if err := sch.Validate(inst); err != nil {
			t.Errorf("row does not match the schema: %v\n%s", err, row)
		}
	}
}

func TestGetUnknownResourceTypeStillErrors(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := d.handleGet(mustMarshal(t, map[string]string{"resource_type": "registers-of-all-seats"}))
	if resp.Error == "" {
		t.Fatal("an unknown resource type was served")
	}
}

// The pull file carries the rows get register serves, byte for byte, under the
// one pass-time key, in a directory made 0700 and a file made 0600.
func TestRegisterPullPassWritesWhatGetRegisterServes(t *testing.T) {
	d := newHandlerDaemon(t)
	registerFixture(t, d)
	d.registerDir = filepath.Join(t.TempDir(), "register")
	at := time.Date(2026, 11, 6, 18, 0, 0, 0, time.UTC)
	if err := d.writeRegisterPull(at); err != nil {
		t.Fatal(err)
	}

	dirInfo, err := os.Stat(d.registerDir)
	if err != nil || dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("directory: %v, %v; want mode 0700", dirInfo, err)
	}
	path := filepath.Join(d.registerDir, register.PullFileName)
	fileInfo, err := os.Stat(path)
	if err != nil || fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("file: %v, %v; want mode 0600", fileInfo, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		WrittenAt time.Time       `json:"written_at"`
		Rows      json.RawMessage `json:"rows"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	_, rpc := getRegister(t, d)
	if !bytes.Equal(file.Rows, rpc) {
		t.Fatalf("pull file rows differ from get register:\nfile %s\nrpc  %s", file.Rows, rpc)
	}
	if !file.WrittenAt.Equal(at) {
		t.Errorf("written_at = %v, want the pass time %v", file.WrittenAt, at)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil || len(top) != 2 {
		t.Errorf("top-level keys = %v, want rows and written_at only", top)
	}
}

// A pass that cannot write returns an error and leaves the earlier file alone;
// the loop logs it and carries on.
func TestRegisterPullPassFailureKeepsTheEarlierFile(t *testing.T) {
	d := newHandlerDaemon(t)
	registerFixture(t, d)
	dir := filepath.Join(t.TempDir(), "register")
	d.registerDir = dir
	if err := d.writeRegisterPull(time.Date(2026, 11, 6, 18, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, register.PullFileName)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := d.writeRegisterPull(time.Date(2026, 11, 6, 18, 1, 0, 0, time.UTC)); err == nil {
		t.Fatal("a pass into a read-only directory reported success")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the earlier file changed after a failed pass: %v", err)
	}
}

// With no layout the pass is off: it writes nothing and reports nothing.
func TestRegisterPullPassIsOffWithoutADirectory(t *testing.T) {
	d := newHandlerDaemon(t)
	d.registerDir = ""
	if err := d.writeRegisterPull(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

// compileRegisterSchema compiles the seat-row schema the register chain
// publishes.
func compileRegisterSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	schemaBytes, err := os.ReadFile("../../contracts/register/seat-row.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaBytes))
	if err != nil {
		t.Fatal(err)
	}
	const id = "https://schema.arcaven.com/marvel/register-seat-row/v1"
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	return sch
}
