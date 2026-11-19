package daemon

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// The wire of each RPC method, pinned as one JSON fixture per method under
// internal/daemonrpc/testdata/rpc. A fixture holds a fully populated request
// (params) or result, written from the types the daemon uses today. The test
// decodes the fixture strictly into the type and encodes it again: a renamed
// json tag, a dropped field, or a field added without a fixture update turns
// the encoded bytes into something other than the fixture, and the test fails.
// This is how the prose compatibility rule of zero-downtime-upgrade.md
// section 6 becomes a check (I-458 row 24).
//
// Regenerate with go test ./internal/daemon -run TestRPCWireGoldens
// -update-rpc, and read the diff: a changed fixture is a wire change.

var updateRPC = flag.Bool("update-rpc", false, "rewrite the RPC wire fixtures from the types in use")

const rpcGoldenDir = "../daemonrpc/testdata/rpc"

// rpcGoldens maps a fixture name, "<method>.params" or "<method>.result", to
// a new value of the type the daemon decodes or returns.
var rpcGoldens = map[string]func() any{
	"logs.params":              func() any { return &daemonrpc.LogsParams{} },
	"logs.result":              func() any { return &logsResult{} },
	"events.params":            func() any { return &daemonrpc.EventsParams{} },
	"events.result":            func() any { return &daemonrpc.EventsBatch{} },
	"apply.params":             func() any { return &daemonrpc.ApplyParams{} },
	"prune_plan.params":        func() any { return &daemonrpc.ApplyParams{} },
	"get.params":               func() any { return &daemonrpc.GetParams{} },
	"describe.params":          func() any { return &daemonrpc.DescribeParams{} },
	"delete.params":            func() any { return &daemonrpc.DeleteParams{} },
	"scale.params":             func() any { return &daemonrpc.ScaleParams{} },
	"converge.params":          func() any { return &daemonrpc.ConvergeParams{} },
	"run.params":               func() any { return &daemonrpc.RunParams{} },
	"shift.params":             func() any { return &daemonrpc.ShiftParams{} },
	"reset-health.params":      func() any { return &daemonrpc.ResetHealthParams{} },
	"inject.params":            func() any { return &daemonrpc.InjectParams{} },
	"capture.params":           func() any { return &daemonrpc.CaptureParams{} },
	"stop.params":              func() any { return &daemonrpc.StopParams{} },
	"reexec.params":            func() any { return &daemonrpc.ReexecParams{} },
	"backend.verify.params":    func() any { return &daemonrpc.BackendVerifyParams{} },
	"credential.put.params":    func() any { return &daemonrpc.CredentialPutParams{} },
	"credential.get.params":    func() any { return &daemonrpc.CredentialNameParams{} },
	"credential.reveal.params": func() any { return &daemonrpc.CredentialNameParams{} },
	"credential.delete.params": func() any { return &daemonrpc.CredentialNameParams{} },
	"credential.reveal.result": func() any { return &credentialRevealResult{} },
	"team.hibernate.params":    func() any { return &daemonrpc.HibernateParams{} },
	"team.wake.params":         func() any { return &daemonrpc.WakeParams{} },
	"team.wake.result":         func() any { return &wakeResult{} },
	"suspend.params":           func() any { return &daemonrpc.SuspendParams{} },
	"suspend.result":           func() any { return &suspendResult{} },
	"resume.result":            func() any { return &resumeResult{} },
	"view.refresh.params":      func() any { return &daemonrpc.ViewRefreshParams{} },
	"exposure.add.params":      func() any { return &daemonrpc.ExposureVerbParams{} },
	"exposure.end.params":      func() any { return &daemonrpc.ExposureVerbParams{} },
	"exposure.void.params":     func() any { return &daemonrpc.ExposureVerbParams{} },
	"bus.audit.params":         func() any { return &daemonrpc.BusAuditParams{} },
	"heartbeat.params":         func() any { return &heartbeatParams{} },
	"bus.beat.params":          func() any { return &api.BusBeatRequest{} },
	"handoff.put.params":       func() any { return &api.HandoffPutRequest{} },
	"handoff.put.result":       func() any { return &api.HandoffPutResult{} },
	"broker.token.params":      func() any { return &api.BrokerTokenRequest{} },
	"broker.token.result":      func() any { return &api.BrokerTokenResponse{} },
	"account.limits.params":    func() any { return &api.AccountLimitsRequest{} },
	"account.series.params":    func() any { return &api.AccountSeriesRequest{} },
	"account.delta.params":     func() any { return &api.AccountDeltaRequest{} },
	"orphans.result":           func() any { return &orphansResult{} },
	"plan.result":              func() any { return &planResult{} },
	"version.result":           func() any { return &BuildInfo{} },
}

func TestRPCWireGoldens(t *testing.T) {
	update := *updateRPC
	names := make([]string, 0, len(rpcGoldens))
	for n := range rpcGoldens {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(rpcGoldenDir, name+".json")
			if update {
				v := rpcGoldens[name]()
				fillSample(reflect.ValueOf(v).Elem(), name)
				b, err := json.MarshalIndent(v, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(rpcGoldenDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("no fixture for %s: %v", name, err)
			}
			v := rpcGoldens[name]()
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(v); err != nil {
				t.Fatalf("the fixture does not decode into %T: %v", v, err)
			}
			got, err := json.MarshalIndent(v, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(append(got, '\n'), raw) {
				t.Errorf("%s: the wire changed.\n got: %s\nwant: %s", name, got, raw)
			}
		})
	}
}

var sampleTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// fillSample sets every field of v to a fixed non-zero value, so an omitempty
// field still shows in the fixture. It is deterministic: the same type always
// gives the same bytes.
func fillSample(v reflect.Value, tag string) {
	switch v.Kind() {
	case reflect.String:
		v.SetString(tag)
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(7)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(7)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1.5)
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fillSample(v.Elem(), tag)
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			v.SetBytes([]byte(tag))
			return
		}
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fillSample(s.Index(0), tag)
		v.Set(s)
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		k := reflect.New(v.Type().Key()).Elem()
		fillSample(k, "k")
		e := reflect.New(v.Type().Elem()).Elem()
		fillSample(e, tag)
		m.SetMapIndex(k, e)
		v.Set(m)
	case reflect.Struct:
		if v.Type() == reflect.TypeOf(time.Time{}) {
			v.Set(reflect.ValueOf(sampleTime))
			return
		}
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if !f.IsExported() || f.Tag.Get("json") == "-" {
				continue
			}
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "" {
				name = f.Name
			}
			fillSample(v.Field(i), name)
		}
	case reflect.Interface:
		// A field typed any or json.RawMessage's interface form is left nil.
	}
}

// Every fixture belongs to an entry of rpcGoldens, so a fixture left behind by
// a method that went away is a failure and not a stale file.
func TestRPCGoldenFixturesAreAllInTheTable(t *testing.T) {
	ents, err := os.ReadDir(rpcGoldenDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		name := strings.TrimSuffix(e.Name(), ".json")
		if rpcGoldens[name] == nil {
			t.Errorf("fixture %s has no entry in rpcGoldens", e.Name())
		}
	}
	if len(ents) != len(rpcGoldens) {
		t.Errorf("%d fixtures for %d table entries", len(ents), len(rpcGoldens))
	}
}
