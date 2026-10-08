package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// Redaction (docs/design/describe-redaction.md, RD-1). A role's declared Env
// values must never leave the daemon on a read method, whichever method built
// the response.

const canary = "canary-1-do-not-print"

const canaryManifest = `
[workspace]
name = "redactws"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "sleep"
    args = ["300"]
    env = { K = "` + canary + `" }
`

// rawResponse is what any socket caller receives for method, as bytes.
func rawResponse(t *testing.T, d *Daemon, method string, params any) string {
	t.Helper()
	var p []byte
	if params != nil {
		p = mustMarshal(t, params)
	}
	resp := d.dispatch(Request{Method: method, Params: p})
	return string(resp.Result) + resp.Error
}

// The raw bytes of get, describe and plan carry the key and never the value.
// plan is the case a verb-by-verb fix misses: a scale-down to 0 puts the whole
// session, runtime included, in RolePlan.Delete.
func TestReadMethodsNeverPrintAnEnvValue(t *testing.T) {
	d := newHandlerDaemon(t)
	if resp := applyManifest(t, d, canaryManifest); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	var sess api.Session
	for _, s := range d.store.ListSessions() {
		if s.Workspace == "redactws" {
			sess = s
		}
	}
	if sess.Name == "" {
		t.Fatal("no session was spawned")
	}
	if err := d.store.UpdateTeam("redactws/squad", func(tm *api.Team) error {
		for i := range tm.Roles {
			tm.Roles[i].Replicas = 0
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	for name, raw := range map[string]string{
		"get sessions":     rawResponse(t, d, "get", map[string]string{"resource_type": "sessions"}),
		"get teams":        rawResponse(t, d, "get", map[string]string{"resource_type": "teams"}),
		"describe session": rawResponse(t, d, "describe", map[string]string{"resource_type": "session", "name": sess.Key()}),
		"describe team":    rawResponse(t, d, "describe", map[string]string{"resource_type": "team", "name": "redactws/squad"}),
		"plan":             rawResponse(t, d, "plan", nil),
	} {
		if strings.Contains(raw, canary) {
			t.Errorf("%s prints the Env value", name)
		}
	}

	// The key is still shown, with the placeholder, so an operator can see what
	// is set. describe session is the one view that carries the whole runtime.
	got := rawResponse(t, d, "describe", map[string]string{"resource_type": "session", "name": sess.Key()})
	if !strings.Contains(got, `"K"`) || !strings.Contains(got, "(redacted)") {
		t.Errorf("describe session shows no key with (redacted):\n%s", got)
	}

	// The durable record keeps the value: redaction is a view, not a rewrite.
	stored, err := d.store.GetSession(sess.Key())
	if err != nil {
		t.Fatal(err)
	}
	if stored.Runtime.Env["K"] != canary {
		t.Errorf("the stored Env value changed: %q", stored.Runtime.Env["K"])
	}
}

// redactionClass says, for every method in the dispatch table, whether its
// response can carry an api.Runtime (so it must go through the redaction view)
// and why not where it cannot. A method added to the table without an entry
// here fails TestEveryDispatchMethodIsClassifiedForRedaction, so a new method
// cannot reach the wire unreviewed.
var redactionClass = map[string]string{
	"get":      "runtime: lists sessions and teams",
	"describe": "runtime: one session or team",
	"plan":     "runtime: RolePlan.Delete carries whole sessions",

	"apply":               "none: status, workspace, advisories, behind count",
	"delete":              "none: status string",
	"scale":               "none: counts",
	"converge":            "none: posture, team keys, role reports",
	"reap":                "none: counts and names",
	"heartbeat":           "none: status string",
	"account.limits":      "none: limit readings",
	"run":                 "none: status and session key",
	"shift":               "none: status strings",
	"reset-health":        "none: counts",
	"inject":              "none: byte counts and attribution",
	"capture":             "none: pane text",
	"credential.put":      "none: credential metadata, Value is json:\"-\"",
	"credential.get":      "none: credential metadata",
	"credential.reveal":   "none: the credential value, local socket only",
	"credential.list":     "none: credential metadata",
	"credential.delete":   "none: name",
	"backend.verify":      "none: BackendVerification rows",
	"bus.status":          "none: broker status",
	"daemon.status":       "none: daemon status",
	"view.refresh":        "none: log lines",
	"bus.leaf.connect":    "none: status",
	"bus.leaf.disconnect": "none: status",
	"stop":                "none: status",
	"reexec":              "none: status and binary path",
	"logs":                "none: log lines",
	"events":              "none: event batch; test R5 keeps Env values out of events",
	"events.watch":        "none: refused here, streamed elsewhere",
	"version":             "none: build info",
	"orphans":             "none: session keys and counts",
}

// dispatchMethods reads the case labels of dispatchAs from the source, so the
// table cannot grow without this test seeing it.
func dispatchMethods(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	consts := map[string]string{}
	var dispatch *ast.FuncDecl
	for _, name := range []string{"daemon.go", "build.go"} {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok || len(vs.Names) != len(vs.Values) {
						continue
					}
					for i, v := range vs.Values {
						if lit, ok := v.(*ast.BasicLit); ok && lit.Kind == token.STRING {
							if val, err := strconv.Unquote(lit.Value); err == nil {
								consts[vs.Names[i].Name] = val
							}
						}
					}
				}
			case *ast.FuncDecl:
				if d.Name.Name == "dispatchAs" {
					dispatch = d
				}
			}
		}
	}
	if dispatch == nil {
		t.Fatal("dispatchAs not found")
	}
	var methods []string
	ast.Inspect(dispatch.Body, func(n ast.Node) bool {
		sw, ok := n.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		if sel, ok := sw.Tag.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Method" {
			return true
		}
		for _, stmt := range sw.Body.List {
			cc, ok := stmt.(*ast.CaseClause)
			if !ok {
				continue
			}
			for _, e := range cc.List {
				switch v := e.(type) {
				case *ast.BasicLit:
					if val, err := strconv.Unquote(v.Value); err == nil {
						methods = append(methods, val)
					}
				case *ast.Ident:
					if val, ok := consts[v.Name]; ok {
						methods = append(methods, val)
					} else {
						t.Fatalf("case label %s is not a string constant the test can read", v.Name)
					}
				}
			}
		}
		return false
	})
	sort.Strings(methods)
	return methods
}

// Every method in the dispatch table is reviewed for whether its response can
// carry an api.Runtime, and no entry outlives its method.
func TestEveryDispatchMethodIsClassifiedForRedaction(t *testing.T) {
	methods := dispatchMethods(t)
	if len(methods) < 25 {
		t.Fatalf("read only %d dispatch methods; the parser lost the table: %v", len(methods), methods)
	}
	inTable := map[string]bool{}
	for _, m := range methods {
		inTable[m] = true
		if redactionClass[m] == "" {
			t.Errorf("method %q is in the dispatch table with no redaction classification", m)
		}
	}
	for m := range redactionClass {
		if !inTable[m] {
			t.Errorf("redactionClass names %q, which is not in the dispatch table", m)
		}
	}
}
