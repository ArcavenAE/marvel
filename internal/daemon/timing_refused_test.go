package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/timing"
)

// onlyOtherSite fails when a refused invented method minted a site of its own,
// and when the refusal was not the one under test.
func onlyOtherSite(t *testing.T, d *Daemon, names ...string) {
	t.Helper()
	sites := rpcSites(t, d)
	if got := sites[timing.RPCSite("other")].Count; got != uint64(len(names)) {
		t.Errorf("rpc.other Count = %d, want %d", got, len(names))
	}
	for _, n := range names {
		if _, ok := sites[timing.RPCSite(n)]; ok {
			t.Errorf("a refused invented method minted the site %q", timing.RPCSite(n))
		}
	}
}

func dispatchInvented(t *testing.T, d *Daemon, c caller, names ...string) {
	t.Helper()
	for _, n := range names {
		if resp := d.dispatchAs(daemonrpc.Request{Method: n}, c); resp.Error == "" {
			t.Fatalf("%s: no refusal, want one", n)
		}
	}
}

func TestInventedMethodRefusedByScopeIsBucketed(t *testing.T) {
	d := statusDaemon(t)
	dispatchInvented(t, d, caller{scope: ScopeCredentialPush, local: true}, "invented-scope-a", "invented-scope-b")
	onlyOtherSite(t, d, "invented-scope-a", "invented-scope-b")
}

func TestInventedMethodRefusedWhileSuspendedIsBucketed(t *testing.T) {
	d := statusDaemon(t)
	d.suspendHeld.Store(true)
	dispatchInvented(t, d, localCaller(), "invented-hold-a", "invented-hold-b")
	onlyOtherSite(t, d, "invented-hold-a", "invented-hold-b")
}

func TestInventedMethodRefusedWhileAdoptingIsBucketed(t *testing.T) {
	d := statusDaemon(t)
	d.adoptingSince.Store(time.Now().UnixNano())
	dispatchInvented(t, d, localCaller(), "invented-adopt-a", "invented-adopt-b")
	onlyOtherSite(t, d, "invented-adopt-a", "invented-adopt-b")
}

// A method the daemon serves keeps its own site even when a refusal answers it,
// so a held daemon still shows which calls clients are making.
func TestAServedMethodRefusedWhileAdoptingKeepsItsOwnSite(t *testing.T) {
	d := statusDaemon(t)
	d.adoptingSince.Store(time.Now().UnixNano())
	if resp := d.dispatchAs(daemonrpc.Request{Method: "get"}, localCaller()); resp.Error == "" {
		t.Fatal("get was not refused while adopting")
	}
	if got := rpcSites(t, d)[timing.RPCSite("get")].Count; got != 1 {
		t.Errorf("rpc.get Count = %d, want 1", got)
	}
}

// servedMethods must equal the string cases of dispatchUntimed's switch, so a
// method added to the switch is timed under its own name and never as "other".
func TestServedMethodsAreTheSwitchCases(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "daemon.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "dispatchUntimed" {
			return true
		}
		ast.Inspect(fd.Body, func(m ast.Node) bool {
			cc, ok := m.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, e := range cc.List {
				if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if s, err := strconv.Unquote(lit.Value); err == nil {
						cases[s] = true
					}
				}
			}
			return true
		})
		return false
	})
	if len(cases) < 20 {
		t.Fatalf("parsed %d switch cases from dispatchUntimed, want the whole switch", len(cases))
	}
	for c := range cases {
		if !servedMethods[c] {
			t.Errorf("the switch serves %q and servedMethods does not list it", c)
		}
	}
	for s := range servedMethods {
		if !cases[s] {
			t.Errorf("servedMethods lists %q and the switch does not serve it", s)
		}
	}
}
