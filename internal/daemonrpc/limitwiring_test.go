package daemonrpc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The daemon refuses to let anything in its own package dial a daemon socket
// (internal/daemon/limitwiring_test.go). The dials moved here with the client,
// so the property "the only dials are the client's" is checked here. The AST
// helpers below are a copy of that file's, not a shared package, until a
// second copy appears.

type pkgFiles struct {
	fset  *token.FileSet
	files map[string]*ast.File
}

func parseClient(t *testing.T) pkgFiles {
	t.Helper()
	names, err := filepath.Glob("*.go")
	if err != nil || len(names) == 0 {
		t.Fatalf("no daemonrpc sources: %v", err)
	}
	p := pkgFiles{fset: token.NewFileSet(), files: map[string]*ast.File{}}
	for _, n := range names {
		if strings.HasSuffix(n, "_test.go") {
			continue
		}
		src, err := os.ReadFile(n)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(p.fset, n, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		p.files[n] = f
	}
	return p
}

func (p pkgFiles) each(fn func(name string, n ast.Node) bool) {
	var names []string
	for n := range p.files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		ast.Inspect(p.files[n], func(x ast.Node) bool { return fn(n, x) })
	}
}

func enclosing(fd *ast.FuncDecl) string {
	if fd.Recv != nil && len(fd.Recv.List) == 1 {
		return types.ExprString(fd.Recv.List[0].Type) + "." + fd.Name.Name
	}
	return fd.Name.Name
}

// eachFunc walks every top-level function of this package, with the
// function's name, so a finding can say where it is.
func (p pkgFiles) eachFunc(fn func(file, fun string, n ast.Node) bool) {
	p.each(func(file string, n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}
		ast.Inspect(fd, func(m ast.Node) bool { return fn(file, enclosing(fd), m) })
		return false
	})
}

// isDeclName reports whether an identifier is the name of a function declaration
// itself, which is not a use.
func isDeclName(p pkgFiles, n ast.Node) bool {
	id, ok := n.(*ast.Ident)
	if !ok {
		return false
	}
	found := false
	p.each(func(file string, m ast.Node) bool {
		if fd, ok := m.(*ast.FuncDecl); ok && fd.Name == id {
			found = true
		}
		return !found
	})
	return found
}

// The client's entry points have exactly the callers listed: SendRequest and
// WatchEventsWith are called by nothing in this package, SendRequestWith by
// SendRequest, and the dial by the two that carry a request. None is used as a
// value.
func TestClientEntryPointsHaveOnlyTheirInventoriedCallers(t *testing.T) {
	t.Parallel()
	want := map[string]map[string]int{
		"SendRequest":     {},
		"SendRequestWith": {"SendRequest": 1},
		"WatchEventsWith": {},
		"dialDaemonWith":  {"SendRequestWith": 1, "WatchEventsWith": 1},
	}
	got := map[string]map[string]int{}
	for name := range want {
		got[name] = map[string]int{}
	}
	p := parseClient(t)
	called := map[ast.Node]bool{}
	p.eachFunc(func(file, fun string, n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		called[c.Fun] = true
		name := ""
		switch f := c.Fun.(type) {
		case *ast.SelectorExpr:
			name = f.Sel.Name
			called[f.Sel] = true
		case *ast.Ident:
			name = f.Name
		}
		if m, ok := got[name]; ok {
			m[fun]++
		}
		return true
	})
	p.each(func(file string, n ast.Node) bool {
		var name string
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if called[x] {
				return true
			}
			name = x.Sel.Name
		case *ast.Ident:
			if called[x] {
				return true
			}
			name = x.Name
		default:
			return true
		}
		if _, tracked := got[name]; tracked && !isDeclName(p, n) {
			t.Errorf("%s: %s used as a value", file, name)
		}
		return true
	})
	for name, w := range want {
		if !reflect.DeepEqual(got[name], w) {
			t.Errorf("callers of %s are %v, inventoried as %v", name, got[name], w)
		}
	}
}

// The only dials in the package are the client's (dialDaemonWith, to the
// daemon the CLI names), the SSH client's one (sshDial, which the tunnel and
// the direct dial share), and the dial to the user's SSH agent; net.Pipe and
// the Dialer type are not used at all, and the package may not spawn commands
// or open HTTP or RPC connections.
func TestTheOnlyDialsAreTheClients(t *testing.T) {
	t.Parallel()
	wantDial := map[string]int{
		"net.DialTimeout in dialDaemonWith": 2,
		"net.Dial in sshAuthMethodsFor":     1,
		"net.DialTimeout in sshDial":        1,
	}
	gotDial := map[string]int{}
	p := parseClient(t)
	p.eachFunc(func(file, fun string, n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		x := types.ExprString(sel.X)
		if x != "net" && x != "ssh" && x != "tls" {
			return true
		}
		if sel.Sel.Name == "Pipe" || sel.Sel.Name == "Dialer" || strings.HasPrefix(sel.Sel.Name, "Dial") {
			gotDial[x+"."+sel.Sel.Name+" in "+fun]++
		}
		return true
	})
	for k, n := range gotDial {
		if wantDial[k] != n {
			t.Errorf("%s: %d uses, inventoried as %d", k, n, wantDial[k])
		}
	}
	for k, n := range wantDial {
		if gotDial[k] != n {
			t.Errorf("inventoried %q: %d uses, now %d", k, n, gotDial[k])
		}
	}
	p.each(func(file string, n ast.Node) bool {
		if imp, ok := n.(*ast.ImportSpec); ok {
			path, _ := strconv.Unquote(imp.Path.Value)
			switch path {
			case "os/exec", "net/http", "net/rpc":
				t.Errorf("%s imports %s; the client opens no other route to a socket or a process", file, path)
			}
		}
		return true
	})
}
