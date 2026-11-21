package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// declaredSets is, for each method that belongs to any set beyond "served",
// the letters of the sets it is declared in. It is written out here, apart from
// the table, so a method added to the table with a wrong or missing column
// fails this test and not a request at run time:
//
//	S answers while the daemon is adopting or held
//	D is answered ErrReportDeferred while adopting
//	P a credential-push key may call it
//	L only the local unix socket may call it
//	F a fan-out across clusters may call it
//	W streams many responses on one connection
var declaredSets = map[string]string{
	daemonrpc.MethodDaemonStatus:      "S",
	daemonrpc.MethodHostVitals:        "SF",
	daemonrpc.MethodSuspendStatus:     "S",
	daemonrpc.MethodGet:               "SF",
	daemonrpc.MethodDescribe:          "S",
	daemonrpc.MethodLogs:              "S",
	daemonrpc.MethodEvents:            "S",
	daemonrpc.MethodBusStatus:         "S",
	daemonrpc.MethodBusAudit:          "S",
	daemonrpc.MethodOrphans:           "S",
	daemonrpc.MethodCredentialList:    "SP",
	daemonrpc.MethodVersion:           "SF",
	daemonrpc.MethodResume:            "SL",
	daemonrpc.MethodHeartbeat:         "D",
	daemonrpc.MethodBusBeat:           "D",
	daemonrpc.MethodAccountLimits:     "D",
	daemonrpc.MethodCredentialPut:     "P",
	daemonrpc.MethodCredentialDelete:  "P",
	daemonrpc.MethodBusLeafConnect:    "P",
	daemonrpc.MethodBusLeafDisconnect: "P",
	daemonrpc.MethodCredentialReveal:  "L",
	daemonrpc.MethodExposureVoid:      "L",
	daemonrpc.MethodBrokerToken:       "L",
	daemonrpc.MethodHandoffPut:        "L",
	daemonrpc.MethodSuspend:           "L",
	daemonrpc.MethodTeamHibernate:     "L",
	daemonrpc.MethodTeamWake:          "L",
	daemonrpc.MethodEventsWatch:       "W",
}

// Every method the table names appears in exactly the sets it declares, and in
// no other.
func TestMethodTableSets(t *testing.T) {
	fan := map[string]bool{}
	for _, m := range FanOutMethods() {
		fan[m] = true
	}
	for name, spec := range methodTable {
		var got strings.Builder
		for _, c := range []struct {
			letter string
			in     bool
		}{
			{"S", startingMethods[name]},
			{"D", reportDeferredMethods[name]},
			{"P", credentialPushMethods[name]},
			{"L", localOnlyMethods[name]},
			{"F", fan[name]},
			{"W", spec.Streams},
		} {
			if c.in {
				got.WriteString(c.letter)
			}
		}
		if want := declaredSets[name]; got.String() != want {
			t.Errorf("%s is in sets %q, declared %q", name, got.String(), want)
		}
		if !servedMethods[name] {
			t.Errorf("%s is in the table and not in servedMethods", name)
		}
		if spec.Handle == nil {
			t.Errorf("%s has no handler", name)
		}
	}
	for name := range declaredSets {
		if _, ok := methodTable[name]; !ok {
			t.Errorf("%s is declared in a set and not in the table", name)
		}
	}
	for name := range servedMethods {
		if _, ok := methodTable[name]; !ok {
			t.Errorf("servedMethods names %s and the table does not", name)
		}
	}
	for name := range startingMethods {
		if !servedMethods[name] {
			t.Errorf("%s answers while adopting and is not served", name)
		}
	}
}

// The table and the constants in daemonrpc name the same methods: a method with
// no constant, or a constant no table entry serves, fails here.
func TestMethodTableNamesMatchTheDaemonrpcConstants(t *testing.T) {
	consts := map[string]bool{daemonrpc.MethodVersion: true, daemonrpc.MethodEventsWatch: true}
	f, err := parser.ParseFile(token.NewFileSet(), "../daemonrpc/methods.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, id := range vs.Names {
			if !strings.HasPrefix(id.Name, "Method") {
				continue
			}
			lit, ok := vs.Values[i].(*ast.BasicLit)
			if !ok {
				t.Errorf("%s is not a string literal", id.Name)
				continue
			}
			v, _ := strconv.Unquote(lit.Value)
			consts[v] = true
		}
		return true
	})
	var missing, extra []string
	for c := range consts {
		if _, ok := methodTable[c]; !ok {
			extra = append(extra, c)
		}
	}
	for m := range methodTable {
		if !consts[m] {
			missing = append(missing, m)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("table methods with no daemonrpc constant: %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("daemonrpc constants with no table entry: %v", extra)
	}
}

// A method outside the table is refused as unknown, as the switch's default
// case did.
func TestUnknownMethodIsRefusedByTheTable(t *testing.T) {
	d := statusDaemon(t)
	resp := d.dispatchUntimed(daemonrpc.Request{Method: "no.such.method"}, localCaller())
	if !strings.HasPrefix(resp.Error, unknownMethodPrefix) {
		t.Errorf("error = %q, want the %q prefix", resp.Error, unknownMethodPrefix)
	}
}
