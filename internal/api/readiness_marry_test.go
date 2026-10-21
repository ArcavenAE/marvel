package api

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// marryVectorsPath is the shared golden file for the marrying step. director
// reads the same file (M2).
var marryVectorsPath = filepath.Join("..", "..", "contracts", "readiness", "marry-vectors.json")

type marryStep struct {
	Op             string          `json:"op"`
	Input          *MarryInput     `json:"input"`
	Lease          *Lease          `json:"lease"`
	Now            time.Time       `json:"now"`
	ReRead         *SendReRead     `json:"reread"`
	Address        string          `json:"address"`
	File           string          `json:"file"`
	AllowedImports []string        `json:"allowed_imports"`
	ForbidLiterals []string        `json:"forbid_literals"`
	ForbidCalls    []string        `json:"forbid_calls"`
	Want           json.RawMessage `json:"want"`
}

type marryVectors struct {
	Version int      `json:"version"`
	Rows    []string `json:"rows"`
	Vectors []struct {
		Name  string      `json:"name"`
		Row   string      `json:"row"`
		Steps []marryStep `json:"steps"`
	} `json:"vectors"`
}

func loadMarryVectors(t *testing.T) marryVectors {
	t.Helper()
	b, err := os.ReadFile(marryVectorsPath)
	if err != nil {
		t.Fatalf("read %s: %v", marryVectorsPath, err)
	}
	var v marryVectors
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("parse %s: %v", marryVectorsPath, err)
	}
	if v.Version != 1 || len(v.Rows) == 0 || len(v.Vectors) == 0 {
		t.Fatalf("%s: version %d, %d rows, %d vectors", marryVectorsPath, v.Version, len(v.Rows), len(v.Vectors))
	}
	return v
}

// TestMarryGoldenVectors runs every step of every shared vector.
func TestMarryGoldenVectors(t *testing.T) {
	t.Parallel()
	for _, vec := range loadMarryVectors(t).Vectors {
		t.Run(vec.Name, func(t *testing.T) {
			t.Parallel()
			if len(vec.Steps) == 0 {
				t.Fatal("no steps")
			}
			for i, st := range vec.Steps {
				runMarryStep(t, strconv.Itoa(i)+" "+st.Op, st)
			}
		})
	}
}

func runMarryStep(t *testing.T, name string, st marryStep) {
	t.Helper()
	switch st.Op {
	case "marry":
		var want MarryOutput
		if err := json.Unmarshal(st.Want, &want); err != nil || st.Input == nil {
			t.Fatalf("step %s: input %v, want %v", name, st.Input != nil, err)
		}
		got, err := Marry(*st.Input)
		if err != nil {
			t.Fatalf("step %s: %v", name, err)
		}
		if !reflect.DeepEqual(got, want) {
			g, _ := json.Marshal(got)
			t.Errorf("step %s\n got %s\nwant %s", name, g, st.Want)
		}
	case "take":
		var want string
		if err := json.Unmarshal(st.Want, &want); err != nil || st.Lease == nil {
			t.Fatalf("step %s: lease %v, want %v", name, st.Lease != nil, err)
		}
		if got := AnswerTake(*st.Lease, st.Now); got != want {
			t.Errorf("step %s: AnswerTake at %s = %q, want %q", name, st.Now.Format(time.RFC3339), got, want)
		}
	case "recheck":
		var want SendCheck
		if err := json.Unmarshal(st.Want, &want); err != nil || st.Lease == nil || st.ReRead == nil {
			t.Fatalf("step %s: lease %v, reread %v, want %v", name, st.Lease != nil, st.ReRead != nil, err)
		}
		if got := RecheckBeforeSend(*st.Lease, *st.ReRead); got != want {
			t.Errorf("step %s: RecheckBeforeSend = %+v, want %+v", name, got, want)
		}
	case "address":
		var want string
		if err := json.Unmarshal(st.Want, &want); err != nil {
			t.Fatalf("step %s: want %v", name, err)
		}
		got := "ok"
		if err := MarryAddress(st.Address); err != nil {
			got = err.Error()
		}
		if got != want {
			t.Errorf("step %s: MarryAddress(%q) = %q, want %q", name, st.Address, got, want)
		}
	case "grep":
		if len(st.AllowedImports) == 0 || len(st.ForbidLiterals) == 0 || len(st.ForbidCalls) == 0 {
			t.Fatalf("step %s: the grep names no imports, literals or calls", name)
		}
		for _, bad := range sendPathFindings(t, filepath.Join("..", "..", st.File), st.AllowedImports, st.ForbidLiterals, st.ForbidCalls) {
			t.Errorf("step %s: %s", name, bad)
		}
	default:
		t.Fatalf("step %s: unknown op", name)
	}
}

// sendPathFindings is the trigger row's grep, read from the syntax tree so
// that prose cannot trip it: an import outside the allowed list, a string
// literal that builds a fan-out address, or a call through a selector named
// for a send.
func sendPathFindings(t *testing.T, path string, allowed, literals, calls []string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Imports) == 0 || len(f.Decls) == 0 {
		t.Fatalf("%s: nothing parsed", path)
	}
	var out []string
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if !slices.Contains(allowed, p) {
			out = append(out, "imports "+p)
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BasicLit:
			if n.Kind != token.STRING {
				break
			}
			for _, l := range literals {
				if strings.Contains(n.Value, l) {
					out = append(out, "a literal builds "+l)
				}
			}
		case *ast.CallExpr:
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok && slices.Contains(calls, sel.Sel.Name) {
				out = append(out, "calls ."+sel.Sel.Name)
			}
		}
		return true
	})
	return out
}

// Positive controls for the grep step: it sees an import, a literal and a
// call, and does not fire on prose.
func TestMarrySendPathGrepSeesEachKind(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		src  string
		want []string
	}{
		"an import":  {"package p\n\nimport \"net\"\n\nvar _ = net.Dial\n", []string{"imports net"}},
		"a literal":  {"package p\n\nimport \"time\"\n\nvar _, a = time.Now, \"role://t/r\"\n", []string{"a literal builds role://"}},
		"a call":     {"package p\n\nimport \"time\"\n\nfunc f(c interface{ Send() }) { _ = time.Now; c.Send() }\n", []string{"calls .Send"}},
		"prose only": {"package p\n\nimport \"time\"\n\n// Send a role:// address via os.Exit.\nfunc RecheckBeforeSend() { _ = time.Now }\n", nil},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "x.go")
			if err := os.WriteFile(path, []byte(tc.src), 0o600); err != nil {
				t.Fatal(err)
			}
			got := sendPathFindings(t, path, []string{"time"}, []string{"role://"}, []string{"Send"})
			if !slices.Equal(got, tc.want) {
				t.Fatalf("findings %q, want %q", got, tc.want)
			}
		})
	}
}

// TestMarryVectorsCoverEveryRow: each row of section 3.2 has a vector, and
// every vector names a row of 3.2 or a part of 3.1.
func TestMarryVectorsCoverEveryRow(t *testing.T) {
	t.Parallel()
	gv := loadMarryVectors(t)
	want := []string{"one item, one holder", "holder mismatch shows", "no fan-out", "strict free", "scope is a filter",
		"re-read before send", "stop word", "one lapse time", "trigger", "client isolation"}
	if !slices.Equal(gv.Rows, want) {
		t.Fatalf("rows = %q, want the ten rows of section 3.2", gv.Rows)
	}
	seen := map[string]int{}
	for _, vec := range gv.Vectors {
		if !slices.Contains(want, vec.Row) && !strings.HasPrefix(vec.Row, "3.1 ") {
			t.Errorf("%s: row %q is not a row of 3.2 or a part of 3.1", vec.Name, vec.Row)
		}
		seen[vec.Row]++
	}
	for _, r := range want {
		if seen[r] != 1 {
			t.Errorf("row %q has %d vectors, want one", r, seen[r])
		}
	}
}

// TestMarryReadsNothingOfAClientItem: a client-scope item contributes its
// count and nothing else. Its id and every other field are absent from the
// output, and changing them changes nothing.
func TestMarryReadsNothingOfAClientItem(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	base := MarryInput{RunID: "r", Now: now, LeaseCap: 3, LeaseMinutes: 30,
		Placement: MarryPlacement{SenderCluster: "c1"}, Rulings: MarryRulings{WorkClassSource: "label"}}
	in := base
	in.Items = []MarryItem{{ID: "client-secret-id", Scope: MarryScopeClient, Classes: map[string]string{"label": "client-secret-class"},
		Component: "client-secret-component", Assignee: "agent://t1/client-secret-holder", Status: "open"}}
	other := base
	other.Items = []MarryItem{{Scope: MarryScopeClient}}
	got, err := Marry(in)
	if err != nil {
		t.Fatal(err)
	}
	want, err := Marry(other)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) || got.Count.Client != 1 {
		t.Fatalf("client item changed the output: %+v, want %+v with client 1", got, want)
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "client-secret") {
		t.Fatalf("output carries the client item: %s", b)
	}
}

// TestMarryRefusesAnInputItCannotRunOn: no run id, no clock, a negative cap
// or a lease with no length are errors, never a run with a guessed value.
func TestMarryRefusesAnInputItCannotRunOn(t *testing.T) {
	t.Parallel()
	ok := MarryInput{RunID: "r", Now: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC), LeaseCap: 1, LeaseMinutes: 30}
	if _, err := Marry(ok); err != nil {
		t.Fatalf("a well-formed input: %v", err)
	}
	for name, mut := range map[string]func(*MarryInput){
		"no run id":    func(in *MarryInput) { in.RunID = "" },
		"no clock":     func(in *MarryInput) { in.Now = time.Time{} },
		"negative cap": func(in *MarryInput) { in.LeaseCap = -1 },
		"no length":    func(in *MarryInput) { in.LeaseMinutes = 0 },
	} {
		in := ok
		mut(&in)
		if _, err := Marry(in); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// TestMarryCountLineNamesEveryField: the count line prints every count of
// section 3.3, leases sent at 0 in shadow.
func TestMarryCountLineNamesEveryField(t *testing.T) {
	t.Parallel()
	c := MarryCount{Ready: 1, Held: 2, Eligible: 3, Unmatched: 4, NoClass: 5, NoComponent: 6, NoEligibleSeat: 7, OperatorOnly: 8, Client: 9,
		Free: 10, Working: 11, Queued: 12, Parked: 13, Unknown: 14, WouldPair: 15, Taken: 17, Declined: 18, Lapsed: 19, Duplicate: 20}
	want := "ready 1, held 2, eligible 3, unmatched 4 (no class 5, no component 6, no eligible seat 7, operator-only 8), client 9; " +
		"seats free 10, working 11, queued 12, parked 13, unknown 14; " +
		"would-pair 15, leases sent 0, taken 17, declined 18, lapsed 19, DUPLICATE 20"
	if got := c.String(); got != want {
		t.Fatalf("count line\n got %s\nwant %s", got, want)
	}
}
