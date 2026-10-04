package daemon

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

// The isolated packages (internal/panemenu, internal/limitact) can reach a pane
// only through what the daemon hands them. This reads EVERY non-test file of the
// daemon package, not a list of files, and pins that hand-off to exact
// expressions, so a new file cannot slip a different sender or capture in
// (marvel#556 review: the first guard enumerated files).

type pkgFiles struct {
	fset  *token.FileSet
	files map[string]*ast.File
}

func parseDaemon(t *testing.T) pkgFiles {
	t.Helper()
	names, err := filepath.Glob("*.go")
	if err != nil || len(names) == 0 {
		t.Fatalf("no daemon sources: %v", err)
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

func isSel(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

// fieldExprs maps a composite literal's keys to their expressions as source text.
func fieldExprs(lit *ast.CompositeLit) map[string]string {
	out := map[string]string{}
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			out["<positional>"] = types.ExprString(el)
			continue
		}
		out[types.ExprString(kv.Key)] = types.ExprString(kv.Value)
	}
	return out
}

func expectFields(t *testing.T, what string, got, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: field %s is %q, pinned as %q", what, k, got[k], v)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("%s: unpinned field %s = %s", what, k, got[k])
		}
	}
}

// limitSender is the only SendKeys the limit action can cause: the constant
// key, not literal, no Enter. limitKey is "2".
func TestLimitSenderIsPinned(t *testing.T) {
	t.Parallel()
	p := parseDaemon(t)
	var keyConst string
	var sendCalls, senderDecls int
	p.each(func(name string, n ast.Node) bool {
		switch x := n.(type) {
		case *ast.ValueSpec:
			if len(x.Names) == 1 && x.Names[0].Name == "limitKey" && len(x.Values) == 1 {
				if lit, ok := x.Values[0].(*ast.BasicLit); ok {
					keyConst, _ = strconv.Unquote(lit.Value)
				}
			}
		case *ast.FuncDecl:
			if x.Name.Name != "limitSender" {
				return true
			}
			senderDecls++
			ast.Inspect(x, func(m ast.Node) bool {
				c, ok := m.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "SendKeys" {
					sendCalls++
					got := make([]string, len(c.Args))
					for i, a := range c.Args {
						got[i] = types.ExprString(a)
					}
					if strings.Join(got, ",") != "paneID,limitKey,false,false" {
						t.Errorf("SendKeys arguments are (%s), pinned as (paneID,limitKey,false,false)", strings.Join(got, ","))
					}
				}
				return true
			})
		}
		return true
	})
	if keyConst != "2" {
		t.Errorf("limitKey = %q, want \"2\"", keyConst)
	}
	if senderDecls != 1 || sendCalls != 1 {
		t.Errorf("limitSender declared %d times with %d SendKeys calls, want 1 and 1", senderDecls, sendCalls)
	}
}

// The action is built once, in newLimitAction, from exactly these four
// expressions, and limitSender is referenced nowhere else. A new file's sender or
// capture cannot be substituted without changing a pinned expression here.
func TestLimitActionIsBuiltOnlyFromThePinnedExpressions(t *testing.T) {
	t.Parallel()
	p := parseDaemon(t)
	var builds, senderUses int
	p.each(func(name string, n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if isSel(x.Fun, "limitact", "New") {
				builds++
				if len(x.Args) != 1 {
					t.Errorf("limitact.New takes one Deps literal")
					return true
				}
				lit, ok := x.Args[0].(*ast.CompositeLit)
				if !ok {
					t.Errorf("%s: limitact.New is not given a literal", name)
					return true
				}
				expectFields(t, "limitact.Deps", fieldExprs(lit), map[string]string{
					"Samples": "d.limitMenu",
					"Events":  "d.events",
					"Send":    "limitSender(d.driver)",
					"Capture": "d.driver.CapturePaneJoined",
				})
			}
		case *ast.Ident:
			if x.Name == "limitSender" {
				senderUses++
			}
		}
		return true
	})
	if builds != 1 {
		t.Errorf("limitact.New is called %d times, want 1", builds)
	}
	if senderUses != 2 { // the declaration and its one use
		t.Errorf("limitSender is named %d times, want 2 (declaration and one use)", senderUses)
	}
}

// The pane-menu source is built once, with pinned fields; its in-front function
// calls d.driver.PaneForeground (a call, not a blank assignment) and no other
// driver method.
func TestPaneMenuSourceIsBuiltOnlyFromThePinnedExpressions(t *testing.T) {
	t.Parallel()
	p := parseDaemon(t)
	builds := 0
	p.each(func(name string, n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || !isSel(lit.Type, "panemenu", "Config") {
			return true
		}
		builds++
		fields := fieldExprs(lit)
		inFront := fields["InFront"]
		delete(fields, "InFront")
		expectFields(t, "panemenu.Config", fields, map[string]string{
			"Samples":  "d.limitMenu",
			"Store":    "d.store",
			"Readings": "d.accounts",
			"Events":   "d.events",
			"Capture":  "d.driver.CapturePaneJoined",
			"Hooks":    "d.newLimitAction().Hooks()",
			"HostZone": "d.hostLocation",
		})
		if inFront == "" {
			t.Error("panemenu.Config has no InFront")
		}
		var fn *ast.FuncLit
		for _, el := range lit.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok && types.ExprString(kv.Key) == "InFront" {
				fn, _ = kv.Value.(*ast.FuncLit)
			}
		}
		if fn == nil {
			t.Error("InFront is not a function literal")
			return true
		}
		called := false
		ast.Inspect(fn, func(m ast.Node) bool {
			if c, ok := m.(*ast.CallExpr); ok && types.ExprString(c.Fun) == "d.driver.PaneForeground" {
				called = true
			}
			if sel, ok := m.(*ast.SelectorExpr); ok {
				if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "driver" && sel.Sel.Name != "PaneForeground" {
					t.Errorf("InFront touches d.driver.%s", sel.Sel.Name)
				}
			}
			return true
		})
		if !called {
			t.Error("InFront does not call d.driver.PaneForeground")
		}
		return true
	})
	if builds != 1 {
		t.Errorf("panemenu.Config is built %d times, want 1", builds)
	}
	newCalls := 0
	p.each(func(name string, n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && isSel(c.Fun, "panemenu", "New") {
			newCalls++
			if len(c.Args) != 1 || types.ExprString(c.Args[0]) != "cfg" {
				t.Errorf("%s: panemenu.New is not called with the one built cfg", name)
			}
		}
		return true
	})
	if newCalls != 1 {
		t.Errorf("panemenu.New is called %d times, want 1", newCalls)
	}
}

// enclosing names the top-level function a node sits in: "Recv.Name" or "Name".
func enclosing(fd *ast.FuncDecl) string {
	if fd.Recv != nil && len(fd.Recv.List) == 1 {
		return types.ExprString(fd.Recv.List[0].Type) + "." + fd.Name.Name
	}
	return fd.Name.Name
}

// eachFunc walks every top-level function of the daemon package, with the
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

// Every place in the daemon that can press a key is listed here, keyed by the
// function it sits in. A new call site anywhere in the package, in a new file, a
// goroutine, an event watcher or a closure assigned to a field, changes the
// count and fails this test (marvel#556 review r4: routes N1, N3, N10 all added a
// SendKeys call). SendKeys is also never used as a value.
func TestEveryKeyPressInTheDaemonIsInventoried(t *testing.T) {
	t.Parallel()
	want := map[string]int{
		"*Daemon.notifyHandoff":  1, // the max-age handoff request, after the pre-flight
		"*Daemon.handleInjectAs": 2, // the operator's inject: clear key, then the text
		"limitSender":            1, // the limit action's digit
	}
	got := map[string]int{}
	p := parseDaemon(t)
	p.eachFunc(func(file, fun string, n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "SendKeys" {
				got[fun]++
			}
		}
		return true
	})
	// A method value (x.SendKeys not called) is a hidden call site.
	calls := map[ast.Node]bool{}
	p.each(func(file string, n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			calls[c.Fun] = true
		}
		return true
	})
	p.each(func(file string, n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "SendKeys" && !calls[sel] {
			t.Errorf("%s: SendKeys used as a value", file)
		}
		return true
	})
	for fun, n := range got {
		if want[fun] != n {
			t.Errorf("%s calls SendKeys %d times, inventoried as %d", fun, n, want[fun])
		}
	}
	for fun, n := range want {
		if got[fun] != n {
			t.Errorf("%s was inventoried with %d SendKeys calls and has %d", fun, n, got[fun])
		}
	}
}

// The driver is reached in the daemon only through these methods, from these
// functions, and escapes as a bare value only into these two. In the two wiring
// files only the two read-only methods appear.
func TestDriverIsReachedOnlyThroughTheInventory(t *testing.T) {
	t.Parallel()
	wantUse := map[string]bool{
		"*Daemon.handleCapture d.driver.CapturePane":        true,
		"*Daemon.handleCapture d.driver.CapturePaneRange":   true,
		"*Daemon.handleInjectAs d.driver.SendKeys":          true,
		"*Daemon.readComposer d.driver.CapturePane":         true,
		"*Daemon.readComposer d.driver.CapturePaneEscapes":  true,
		"*Daemon.repaintSettled d.driver.Repaint":           true,
		"*Daemon.notifyHandoff d.driver.SendKeys":           true,
		"*Daemon.startWatchdog d.driver.PaneForeground":     true,
		"*Daemon.startWatchdog d.driver.CapturePaneJoined":  true,
		"preflightRefusal driver.CapturePaneJoined":         true,
		"bareDigitRefusal driver.CapturePaneEscapes":        true,
		"limitSender drv.SendKeys":                          true,
		"*Daemon.newLimitAction d.driver.CapturePaneJoined": true,
		"*Daemon.paneMenuSource d.driver.CapturePaneJoined": true,
		"*Daemon.paneMenuSource d.driver.PaneForeground":    true,
	}
	wantValue := map[string]bool{
		"*Daemon.handleInjectAs": true, // preflightRefusal(d.driver, ...)
		"*Daemon.notifyHandoff":  true, // preflightRefusal(d.driver, ...)
		"*Daemon.newLimitAction": true, // limitSender(d.driver)
	}
	wiring := map[string]bool{"CapturePaneJoined": true, "PaneForeground": true}
	p := parseDaemon(t)
	used := map[string]bool{}
	values := map[string]bool{}
	p.each(func(file string, n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}
		fun := enclosing(fd)
		selOfSel := map[ast.Node]bool{}
		ast.Inspect(fd, func(m ast.Node) bool {
			sel, ok := m.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x := types.ExprString(sel.X); x == "d.driver" || x == "driver" || x == "drv" {
				key := fun + " " + x + "." + sel.Sel.Name
				used[key] = true
				if !wantUse[key] {
					t.Errorf("%s: %s is not in the driver inventory", file, key)
				}
				if (file == "panemenu.go" || file == "limitaction.go") && x == "d.driver" && !wiring[sel.Sel.Name] {
					t.Errorf("%s: wiring file calls d.driver.%s; only CapturePaneJoined and PaneForeground", file, sel.Sel.Name)
				}
				selOfSel[sel.X] = true
			}
			return true
		})
		ast.Inspect(fd, func(m ast.Node) bool {
			if sel, ok := m.(*ast.SelectorExpr); ok && types.ExprString(sel) == "d.driver" && !selOfSel[sel] {
				values[fun] = true
				if !wantValue[fun] {
					t.Errorf("%s: d.driver escapes as a value in %s", file, fun)
				}
			}
			return true
		})
		return false
	})
	for k := range wantUse {
		if !used[k] {
			t.Errorf("inventoried driver use %q is gone; update the inventory", k)
		}
	}
	for k := range wantValue {
		if !values[k] {
			t.Errorf("inventoried driver value use in %s is gone", k)
		}
	}
}

// The pane-menu source and the limit action are not addressable from the daemon
// once built. Nothing in the package assigns through d.paneMenu or d.limitAct,
// the daemon holds no field typed as the source or the action, and the only
// assignments to the source's wiring fields are the pinned lines in
// paneMenuSource (marvel#556 review r4: routes N3 and N10 reassigned a field of
// the built source from a new file).
func TestBuiltSourceAndActionCannotBeReassigned(t *testing.T) {
	t.Parallel()
	p := parseDaemon(t)
	wiringFields := map[string]bool{"Hooks": true, "Capture": true, "InFront": true, "Send": true, "Matched": true, "Seen": true, "Cleared": true}
	pinned := map[string]string{"d.paneMenu": "panemenu.New(cfg)"}
	seen := map[string]int{}
	check := func(file, fun string, lhs, rhs ast.Expr) {
		l := types.ExprString(lhs)
		root := l
		if i := strings.IndexAny(l, ".[("); i >= 0 {
			if strings.HasPrefix(l, "d.") {
				if j := strings.IndexAny(l[2:], ".[("); j >= 0 {
					root = l[:2+j]
				}
			} else {
				root = l[:i]
			}
		}
		banned := root == "d.paneMenu" || root == "d.limitAct"
		if sel, ok := lhs.(*ast.SelectorExpr); ok && wiringFields[sel.Sel.Name] {
			banned = true
		}
		if !banned {
			return
		}
		want, ok := pinned[l]
		if !ok || fun != "*Daemon.paneMenuSource" || file != "panemenu.go" || rhs == nil || types.ExprString(rhs) != want {
			t.Errorf("%s: %s assigns %s; only the pinned lines in paneMenuSource may assign the source", file, fun, l)
			return
		}
		seen[l]++
	}
	p.eachFunc(func(file, fun string, n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			for i, l := range x.Lhs {
				var r ast.Expr
				if len(x.Lhs) == len(x.Rhs) {
					r = x.Rhs[i]
				}
				check(file, fun, l, r)
			}
		case *ast.IncDecStmt:
			check(file, fun, x.X, nil)
		}
		return true
	})
	for k := range pinned {
		if seen[k] != 1 {
			t.Errorf("pinned assignment %s appears %d times, want 1", k, seen[k])
		}
	}

	rt := reflect.TypeOf(Daemon{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		ts := f.Type.String()
		if strings.Contains(ts, "limitact.") || strings.Contains(ts, "panemenu.Config") {
			t.Errorf("Daemon.%s is typed %s; the daemon may hold neither the source nor the action", f.Name, ts)
		}
	}
	pm, ok := rt.FieldByName("paneMenu")
	if !ok || pm.Type.Kind() != reflect.Interface || pm.Type.NumMethod() != 1 || pm.Type.Method(0).Name != "Evaluate" {
		t.Errorf("Daemon.paneMenu must be an interface with Evaluate only, is %v", pm.Type)
	}
	if _, ok := rt.FieldByName("limitAct"); ok {
		t.Error("Daemon has a limitAct field again")
	}
}

// Nothing in the daemon can get behind the built source or the action by
// reflection, unsafe pointers or type assertions, and the daemon names only the
// constructors and the input types of the two packages (marvel#556 review r5:
// pointer, reflect and assertion routes). The source's own type is unexported, so
// there is nothing to assert to; this keeps it so.
func TestDaemonCannotReachBehindTheBuiltSourceOrAction(t *testing.T) {
	t.Parallel()
	p := parseDaemon(t)
	allowed := map[string]map[string]bool{
		"panemenu": {"Config": true, "New": true, "Samples": true, "Evaluator": true},
		"limitact": {"New": true, "Deps": true},
	}
	p.each(func(file string, n ast.Node) bool {
		switch x := n.(type) {
		case *ast.ImportSpec:
			path, _ := strconv.Unquote(x.Path.Value)
			if path == "reflect" || path == "unsafe" {
				t.Errorf("%s imports %s; the daemon package may not use reflection or unsafe pointers", file, path)
			}
		case *ast.TypeAssertExpr:
			if x.Type != nil {
				if ts := types.ExprString(x.Type); strings.Contains(ts, "panemenu.") || strings.Contains(ts, "limitact.") {
					t.Errorf("%s asserts to %s", file, ts)
				}
			}
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok {
				if names, ok := allowed[id.Name]; ok && !names[x.Sel.Name] && (file != "limitaction.go" || id.Name+"."+x.Sel.Name != "limitact.Action") {
					t.Errorf("%s names %s.%s; the daemon may name only %v", file, id.Name, x.Sel.Name, keysOf(names))
				}
			}
		}
		return true
	})
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// The existing inject path is the other way to type a string into a pane, and a
// hook or a watcher could reach it with "3": by calling the handler, by feeding a
// request to the connection handlers over a pipe, or by dialing the daemon's own
// socket as a client. Every function on that road has an inventory of callers
// (marvel#556 review r5 and r6: routes N1c, W2, W3). The handler is reached only
// from the dispatcher, the dispatcher only from the connection reader, the
// connection handlers only from the listener and the SSH server, and the client
// entry points from nothing in this package. None is used as a value.
func TestInjectPathHasOnlyItsInventoriedCallers(t *testing.T) {
	t.Parallel()
	want := map[string]map[string]int{
		"handleInjectAs":  {"*Daemon.dispatchAs": 1},
		"dispatchAs":      {"*Daemon.handleRWCAs": 1, "*Daemon.dispatch": 1},
		"dispatch":        {},
		"handleRWCAs":     {"*Daemon.handleConn": 1, "*Daemon.handleRWC": 1, "*SSHServer.handleConnection": 1},
		"handleRWC":       {},
		"handleConn":      {"*Daemon.Start": 1},
		"SendRequest":     {},
		"SendRequestWith": {"SendRequest": 1},
		"WatchEventsWith": {},
		"dialDaemonWith":  {"SendRequestWith": 1, "WatchEventsWith": 1},
		// Two functions in other packages that type literal text plus Enter into a
		// pane: the max-age sender the controller holds, and a runtime instance's
		// Inject. The daemon package calls neither.
		"Notify": {},
		"Inject": {},
	}
	got := map[string]map[string]int{}
	for name := range want {
		got[name] = map[string]int{}
	}
	p := parseDaemon(t)
	called := map[ast.Node]bool{}
	// A write to the controller's Notify field installs the sender; it is not a
	// call and not a read. What the installed function can do is covered by the
	// key-press inventory. Exactly one write is allowed, pinned by file, function
	// and expression (teamCtrl.Notify = d.notifyHandoff in the daemon
	// constructor), not by the field's name: any other assignment installs a
	// second sender the inventory does not know.
	notifyWrites := 0
	p.eachFunc(func(file, fun string, n ast.Node) bool {
		if as, ok := n.(*ast.AssignStmt); ok {
			for i, l := range as.Lhs {
				sel, ok := l.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Notify" {
					continue
				}
				notifyWrites++
				if file != "daemon.go" || fun != "NewWithOptions" || types.ExprString(l) != "teamCtrl.Notify" ||
					len(as.Rhs) != len(as.Lhs) || types.ExprString(as.Rhs[i]) != "d.notifyHandoff" {
					t.Errorf("%s: %s writes %s = %s; the only allowed write is teamCtrl.Notify = d.notifyHandoff in daemon.go NewWithOptions", file, fun, types.ExprString(l), types.ExprString(as.Rhs[min(i, len(as.Rhs)-1)]))
				}
				called[sel] = true
				called[sel.Sel] = true
			}
		}
		return true
	})
	defer func() {
		if notifyWrites != 1 {
			t.Errorf("%d writes to a Notify field, want exactly the one pinned", notifyWrites)
		}
	}()
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

// A connection into the daemon's own socket, or a pipe into its handlers, is a
// way to send it an inject request. The only dials in the package are the
// client's (dialDaemonWith, to the daemon the CLI names), the SSH client's two,
// and the dial to the user's SSH agent; net.Pipe and the Dialer type are not
// used at all, and the process may not spawn commands or open HTTP or RPC
// connections.
func TestNoNewRouteIntoTheDaemonsOwnSocket(t *testing.T) {
	t.Parallel()
	wantDial := map[string]int{
		"net.Dial in dialDaemonWith":    2,
		"net.Dial in sshAuthMethodsFor": 1,
		"ssh.Dial in dialSSHDirect":     1,
		"ssh.Dial in dialSSHTunnel":     1,
	}
	gotDial := map[string]int{}
	p := parseDaemon(t)
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
				t.Errorf("%s imports %s; the daemon package opens no other route to a socket or a process", file, path)
			}
		}
		return true
	})
}
