package tmux

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// tmux resolves a bare target by prefix, and by more than one kind of name: a
// session name matches the start of another session's name, and a window name
// can be read as the session (marvel#737). The driver names a session with
// `=<name>` where the command takes a session, and `=<name>:` where it takes a
// window or pane, so the name is matched whole and only as a session.

func paneSession(t *testing.T, d *Driver, pane string) string {
	t.Helper()
	out, err := d.cmd("display-message", "-p", "-t", pane, "#{session_name}").Output()
	if err != nil {
		t.Fatalf("session of %s: %v", pane, err)
	}
	return strings.TrimSpace(string(out))
}

// A window whose name starts with the session's name took the new window's
// target: `new-window -t s` read s as the window "sh" and failed, and in the
// other direction `list-panes -s` and `set-option` acted on another session.
func TestNewPaneAtIgnoresAWindowNamePrefix(t *testing.T) {
	t.Run("the shell's window name starts with the session name", func(t *testing.T) {
		d := utf8Driver(t)
		t.Setenv("SHELL", "/bin/sh")
		if err := d.NewSession("s"); err != nil {
			t.Fatalf("new session: %v", err)
		}
		pane, err := d.NewPane("s", "sleep 300", "seat", nil, false)
		if err != nil {
			t.Fatalf("new pane in a session whose first window is named sh: %v", err)
		}
		if got := paneSession(t, d, pane); got != "s" {
			t.Errorf("pane landed in %q, want s", got)
		}
	})
	t.Run("a seat window named after the workspace session", func(t *testing.T) {
		d := utf8Driver(t)
		if out, err := d.cmd("new-session", "-d", "-s", "marvel-ws", "-n", "marvel-ws-seat0").CombinedOutput(); err != nil {
			t.Fatalf("new-session: %s: %v", out, err)
		}
		pane, err := d.NewPane("marvel-ws", "sleep 300", "seat1", nil, false)
		if err != nil {
			t.Fatalf("new pane in marvel-ws: %v", err)
		}
		if got := paneSession(t, d, pane); got != "marvel-ws" {
			t.Errorf("pane landed in %q, want marvel-ws", got)
		}
	})
	t.Run("an exact window name still works", func(t *testing.T) {
		d := utf8Driver(t)
		if err := d.NewSession("x"); err != nil {
			t.Fatalf("new session: %v", err)
		}
		pane, err := d.NewPane("x", "sleep 300", "x", nil, false)
		if err != nil {
			t.Fatalf("new pane whose window is named like its session: %v", err)
		}
		if got := paneSession(t, d, pane); got != "x" {
			t.Errorf("pane landed in %q, want x", got)
		}
		st, err := d.PaneStatus(pane)
		if err != nil || !st.Exists || !st.Created {
			t.Errorf("PaneStatus = %+v, %v; want the pane marvel made", st, err)
		}
	})
}

// Another session's window named sx must not capture commands aimed at s.
// tmux reads a bare s as the start of that window's name when o is the
// current session, measured on tmux 3.7b and 3.4.
func TestSessionCommandsIgnoreAnotherSessionsWindowName(t *testing.T) {
	d := utf8Driver(t)
	if err := d.NewSession("s"); err != nil {
		t.Fatalf("new session s: %v", err)
	}
	pane, err := d.NewPane("s", "sleep 300", "seat", nil, false)
	if err != nil {
		t.Fatalf("new pane: %v", err)
	}
	// o is made last, so it is the session tmux treats as current, which is
	// when it reads a bare s as the start of o's window name.
	if out, err := d.cmd("new-session", "-d", "-s", "o", "-n", "sx").CombinedOutput(); err != nil {
		t.Fatalf("new-session o: %s: %v", out, err)
	}
	want := strconv.Itoa(DefaultHistoryLimit)
	if got, err := d.ShowOption("s", "history-limit"); err != nil || got != want {
		t.Errorf("history-limit of s = %q, %v; want %s (the raise went to s)", got, err, want)
	}
	if got, err := d.ShowOption("o", "history-limit"); err != nil || got == want {
		t.Errorf("history-limit of o = %q, %v; the raise for s must not reach o", got, err)
	}
	panes, err := d.ListPanes("s")
	if err != nil {
		t.Fatalf("list panes: %v", err)
	}
	var ids []string
	for _, p := range panes {
		ids = append(ids, p.ID)
		if got := paneSession(t, d, p.ID); got != "s" {
			t.Errorf("ListPanes(s) returned %s from session %q", p.ID, got)
		}
	}
	if !slices.Contains(ids, pane) {
		t.Errorf("ListPanes(s) = %v, want it to hold %s", ids, pane)
	}
}

// With only sab present, the name s is absent. A bare `-t s` matched sab by
// prefix: has-session said yes, and kill-session killed it.
func TestAbsentSessionIsNotReadAsALongerName(t *testing.T) {
	d := utf8Driver(t)
	if err := d.NewSession("sab"); err != nil {
		t.Fatalf("new session: %v", err)
	}
	pane, err := d.NewPane("sab", "sleep 300", "seat", nil, false)
	if err != nil {
		t.Fatalf("new pane: %v", err)
	}
	if ok, err := d.sessionExists("s"); err != nil || ok {
		t.Errorf("sessionExists(s) = %v, %v; want false, nil with only sab present", ok, err)
	}
	if err := d.KillSession("s"); err != nil {
		t.Errorf("KillSession(s): %v", err)
	}
	sessions, err := d.ListSessions()
	if err != nil || !slices.Contains(sessions, "sab") {
		t.Fatalf("sessions = %v, %v; killing the absent s must leave sab running", sessions, err)
	}
	if !d.HasPane(pane) {
		t.Error("sab's pane was killed along with the absent s")
	}
	if _, err := d.NewPane("s", "sleep 300", "seat", nil, false); err == nil {
		t.Error("NewPane in the absent session s succeeded, in sab")
	}
	if panes, err := d.ListPanes("s"); err == nil {
		t.Errorf("ListPanes(s) = %v, nil; want an error for the absent session", panes)
	}
	if _, err := d.ShowOption("s", "history-limit"); err == nil {
		t.Error("ShowOption(s) read sab's option")
	}
}

// Workspaces aae and aae2 give the sessions marvel-aae and marvel-aae2. With
// marvel-aae absent, nothing aimed at it may reach marvel-aae2.
func TestAbsentWorkspaceSessionLeavesALongerOneRunning(t *testing.T) {
	d := utf8Driver(t)
	if err := d.NewSession("marvel-aae2"); err != nil {
		t.Fatalf("new session: %v", err)
	}
	pane, err := d.NewPane("marvel-aae2", "sleep 300", "seat", nil, false)
	if err != nil {
		t.Fatalf("new pane: %v", err)
	}
	if ok, err := d.sessionExists("marvel-aae"); err != nil || ok {
		t.Errorf("sessionExists(marvel-aae) = %v, %v; want false, nil", ok, err)
	}
	if err := d.KillSession("marvel-aae"); err != nil {
		t.Errorf("KillSession(marvel-aae): %v", err)
	}
	sessions, err := d.ListSessions()
	if err != nil || !slices.Contains(sessions, "marvel-aae2") {
		t.Fatalf("sessions = %v, %v; marvel-aae2 must still run", sessions, err)
	}
	if !d.HasPane(pane) {
		t.Error("marvel-aae2's pane did not survive the kill of marvel-aae")
	}
}

// The #726 classifier reads tmux's not-found wording for a missing session, so
// a missing `=name` must still say "can't find session". Measured byte for
// byte as `can't find session: <name>` on tmux 3.7b (macOS) and 3.4 (Linux).
func TestHasSessionOnAMissingExactTargetKeepsTheNotFoundWording(t *testing.T) {
	d := utf8Driver(t)
	if err := d.NewSession("present"); err != nil {
		t.Fatalf("new session: %v", err)
	}
	out, err := d.cmd("has-session", "-t", "=missing").CombinedOutput()
	if err == nil {
		t.Fatal("has-session on a missing exact name succeeded")
	}
	if got := strings.TrimSpace(string(out)); got != "can't find session: missing" {
		t.Errorf("has-session text = %q, want %q", got, "can't find session: missing")
	}
	if ok, err := d.sessionExists("missing"); err != nil || ok {
		t.Errorf("sessionExists(missing) = %v, %v; want false, nil", ok, err)
	}
}

// sessionTargetAllow is the escape hatch for a documented false positive: a
// function name mapped to the one-line reason the guard skips it. It is empty
// because driver.go has none. An entry with no reason, or one that names a
// function driver.go does not have, fails the test.
var sessionTargetAllow = map[string]string{}

// paneEntryPoints are the driver functions whose paneID parameter is a pane id
// by contract: their callers hand them the id tmux gave out. A paneID parameter
// anywhere else is not trusted, because a helper that takes a session name and
// calls the parameter paneID would put it on -t. A new pane-taking function is
// added here on purpose, in review.
var paneEntryPoints = map[string]bool{
	"HasPane": true, "PaneStatus": true, "PanePID": true, "KillPane": true,
	"SendKeys": true, "CapturePane": true, "CapturePaneJoined": true,
	"CapturePaneEscapes": true, "Repaint": true, "CapturePaneRange": true,
	"CapturePaneRangeEscapes": true, "PaneForeground": true, "paneStillAt": true,
}

// flagCluster is a short-flag cluster: -t, -dt, -dPt. gluedT is the start of a
// value that has a t among its flags and goes on, so tmux takes what follows
// as the t's argument.
var (
	flagCluster = regexp.MustCompile(`^-[A-Za-z]+$`)
	gluedT      = regexp.MustCompile(`^-[A-Za-z]*t`)
)

// emptyImporter gives every import an empty package. The guard type-checks
// one file, so names from other packages and files stay unresolved and their
// errors are ignored; what it needs, the file's own functions, parameters,
// locals and constants, resolve.
type emptyImporter struct{}

func (emptyImporter) Import(path string) (*types.Package, error) {
	pkg := types.NewPackage(path, path[strings.LastIndex(path, "/")+1:])
	pkg.MarkComplete()
	return pkg, nil
}

// guardViolations lists where the tmux argument lists in src pass something
// other than a pane id or a call to the package's sessionTarget or sessionScope
// to -t, or glue anything onto -t. It reads go/types' name resolution, so a
// constant or variable holding "-t" counts as -t, a local that shadows a helper
// or paneID is not the helper or the pane id, and an identifier is a pane id
// only when it is a parameter named paneID that the function never assigns and
// the function is one of paneEntryPoints.
//
// Known limits, not covered, each of which needs a person to read the diff:
// method values such as `run := d.cmd` and wrapper methods around cmd (only
// calls named cmd or append are scanned); strings.Fields assembling the
// arguments; an args slice filled by index; and exec.Command("tmux") outside
// the driver, which is a different invariant and would be its own guard. A
// false positive goes in sessionTargetAllow with a one-line reason.
func guardViolations(t *testing.T, src any) []string {
	t.Helper()
	v, _ := guardScan(t, src, nil)
	return v
}

// guardStats counts the -t sites the scan checked, by what they take.
type guardStats struct{ sites, sessionTarget, sessionScope, paneID int }

// guardScan is guardViolations with an allowlist: allow maps a function name
// to the one-line reason the guard skips it (a documented false positive).
func guardScan(t *testing.T, src any, allow map[string]string) ([]string, guardStats) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "driver.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{},
		Defs:  map[*ast.Ident]types.Object{},
		Uses:  map[*ast.Ident]types.Object{},
	}
	conf := types.Config{Importer: emptyImporter{}, Error: func(error) {}}
	pkg, _ := conf.Check("tmux", fset, []*ast.File{f}, info)

	var out []string
	var stats guardStats
	report := func(n ast.Node, format string, args ...any) {
		out = append(out, fset.Position(n.Pos()).String()+": "+fmt.Sprintf(format, args...))
	}

	// inits maps a variable to the expression it was first given.
	inits := map[types.Object]ast.Expr{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			if x.Tok == token.DEFINE && len(x.Lhs) == len(x.Rhs) {
				for i, l := range x.Lhs {
					if id, ok := l.(*ast.Ident); ok && info.Defs[id] != nil {
						inits[info.Defs[id]] = x.Rhs[i]
					}
				}
			}
		case *ast.ValueSpec:
			for i, id := range x.Names {
				if i < len(x.Values) && info.Defs[id] != nil {
					inits[info.Defs[id]] = x.Values[i]
				}
			}
		}
		return true
	})

	// full is the whole string value of e when every part of it is known.
	var full func(e ast.Expr, depth int) (string, bool)
	full = func(e ast.Expr, depth int) (string, bool) {
		if tv, ok := info.Types[e]; ok && tv.Value != nil && tv.Value.Kind() == constant.String {
			return constant.StringVal(tv.Value), true
		}
		switch x := e.(type) {
		case *ast.BasicLit:
			if x.Kind == token.STRING {
				v, err := strconv.Unquote(x.Value)
				return v, err == nil
			}
		case *ast.Ident:
			if init, ok := inits[info.Uses[x]]; ok && depth < 8 {
				return full(init, depth+1)
			}
		case *ast.BinaryExpr:
			if x.Op == token.ADD {
				l, lok := full(x.X, depth)
				r, rok := full(x.Y, depth)
				return l + r, lok && rok
			}
		case *ast.ParenExpr:
			return full(x.X, depth)
		}
		return "", false
	}
	// lead is the start of e's string value, as far as it is known.
	var lead func(e ast.Expr, depth int) string
	lead = func(e ast.Expr, depth int) string {
		if v, ok := full(e, 0); ok {
			return v
		}
		if depth > 8 {
			return ""
		}
		switch x := e.(type) {
		case *ast.Ident:
			if init, ok := inits[info.Uses[x]]; ok {
				return lead(init, depth+1)
			}
		case *ast.BinaryExpr:
			if x.Op == token.ADD {
				return lead(x.X, depth+1)
			}
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Sprintf" && len(x.Args) > 0 {
				return lead(x.Args[0], depth+1)
			}
		}
		return ""
	}
	// isTrimmedOut matches strings.TrimSpace(string(out)).
	isTrimmedOut := func(e ast.Expr) bool {
		call, ok := e.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "TrimSpace" {
			return false
		}
		if pk, ok := sel.X.(*ast.Ident); !ok || pk.Name != "strings" {
			return false
		}
		conv, ok := call.Args[0].(*ast.CallExpr)
		if !ok || len(conv.Args) != 1 {
			return false
		}
		if fn, ok := conv.Fun.(*ast.Ident); !ok || fn.Name != "string" {
			return false
		}
		o, ok := conv.Args[0].(*ast.Ident)
		return ok && o.Name == "out"
	}
	// pkgFunc is true for a call to this package's own sessionTarget or
	// sessionScope, not to a local of the same name.
	pkgFunc := func(id *ast.Ident) bool {
		if id.Name != "sessionTarget" && id.Name != "sessionScope" {
			return false
		}
		fn, ok := info.Uses[id].(*types.Func)
		return ok && pkg != nil && fn.Pkg() == pkg && fn.Parent() == pkg.Scope()
	}

	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil || allow[fd.Name.Name] != "" {
			continue
		}
		// paneIDs are the function's own parameters named paneID that nothing
		// in the body assigns or takes the address of.
		paneIDs := map[types.Object]bool{}
		if fd.Type.Params != nil && paneEntryPoints[fd.Name.Name] {
			for _, fld := range fd.Type.Params.List {
				for _, n := range fld.Names {
					if o := info.Defs[n]; n.Name == "paneID" && o != nil {
						paneIDs[o] = true
					}
				}
			}
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.AssignStmt:
				// Any assignment, `=` or `:=` (which go/types reads as a plain
				// assignment when the name is already in scope), stops the
				// parameter being a pane id.
				for _, l := range x.Lhs {
					if id, ok := l.(*ast.Ident); ok {
						delete(paneIDs, info.ObjectOf(id))
					}
				}
			case *ast.UnaryExpr:
				if id, ok := x.X.(*ast.Ident); ok && x.Op == token.AND {
					delete(paneIDs, info.ObjectOf(id))
				}
			}
			return true
		})
		// NewPaneAt is the one function that makes a pane id: it reads it from
		// the -P output of new-window, `paneID := strings.TrimSpace(string(out))`.
		if fd.Name.Name == "NewPaneAt" {
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				as, ok := n.(*ast.AssignStmt)
				if !ok || as.Tok != token.DEFINE || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
					return true
				}
				id, ok := as.Lhs[0].(*ast.Ident)
				if !ok || id.Name != "paneID" || info.Defs[id] == nil || !isTrimmedOut(as.Rhs[0]) {
					return true
				}
				paneIDs[info.Defs[id]] = true
				return true
			})
		}

		// Whoever calls an entry point hands it a pane id: the first argument
		// must be this function's own pane id, not a name that came from
		// elsewhere.
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			callee := ""
			switch fun := call.Fun.(type) {
			case *ast.SelectorExpr:
				callee = fun.Sel.Name
			case *ast.Ident:
				callee = fun.Name
			}
			if !paneEntryPoints[callee] {
				return true
			}
			if id, ok := call.Args[0].(*ast.Ident); !ok || !paneIDs[info.Uses[id]] {
				report(call.Args[0], "%s takes a pane id, and this argument is not one of this function's", callee)
			}
			return true
		})

		// target checks the argument that follows a -t.
		target := func(list []ast.Expr, i int, at ast.Expr) {
			stats.sites++
			if i >= len(list) {
				report(at, "-t with no target")
				return
			}
			switch arg := list[i].(type) {
			case *ast.CallExpr:
				id, ok := arg.Fun.(*ast.Ident)
				switch {
				case !ok || !pkgFunc(id):
					report(arg, "-t takes a call that is not the package's sessionTarget or sessionScope")
				case id.Name == "sessionTarget":
					stats.sessionTarget++
				default:
					stats.sessionScope++
				}
			case *ast.Ident:
				if !paneIDs[info.Uses[arg]] {
					report(arg, "-t takes %s, which is not a pane id parameter or a sessionTarget or sessionScope call", arg.Name)
				} else {
					stats.paneID++
				}
			default:
				report(arg, "-t takes a form the guard does not know")
			}
		}
		check := func(list []ast.Expr) {
			for i, e := range list {
				if v, ok := full(e, 0); ok && flagCluster.MatchString(v) {
					// -t, or a cluster such as -dt: when the t is last it takes
					// the next argument; anywhere else the rest of the cluster
					// is its argument, glued.
					switch ti := strings.IndexByte(v, 't'); {
					case ti < 0:
					case ti == len(v)-1:
						target(list, i+1, e)
					default:
						report(e, "%q is -t glued to what follows: tmux reads it as a target too", v)
					}
					continue
				}
				if l := lead(e, 0); gluedT.MatchString(l) {
					report(e, "-t glued to what follows (%q): tmux reads it as a target too", l)
				}
			}
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				isArgs := false
				switch fun := x.Fun.(type) {
				case *ast.SelectorExpr:
					isArgs = fun.Sel.Name == "cmd"
				case *ast.Ident:
					isArgs = fun.Name == "append"
				}
				if isArgs {
					check(x.Args)
				}
			case *ast.CompositeLit:
				check(x.Elts)
			}
			return true
		})
	}
	return out, stats
}

// allowlistProblems says what is wrong with an allowlist: an entry with no
// reason, or one that names a function src does not have.
func allowlistProblems(src string, allow map[string]string) []string {
	var out []string
	for fn, reason := range allow {
		if strings.TrimSpace(reason) == "" {
			out = append(out, fmt.Sprintf("sessionTargetAllow[%q] has no reason", fn))
		}
		if !strings.Contains(src, "func (d *Driver) "+fn+"(") && !strings.Contains(src, "func "+fn+"(") {
			out = append(out, fmt.Sprintf("sessionTargetAllow names %q, which driver.go does not have", fn))
		}
	}
	return out
}

// The allowlist checks run over the real, empty list in the test below, so
// they are tried here on a fixture: each fault is reported, and a good entry
// is not.
func TestAllowlistChecksReportWhatTheyShould(t *testing.T) {
	const src = "package tmux\nfunc (d *Driver) Real() {}\nfunc plain() {}\n"
	if got := allowlistProblems(src, map[string]string{"Real": "hand-built exact form", "plain": "a free function"}); len(got) != 0 {
		t.Errorf("good entries were reported: %v", got)
	}
	if got := allowlistProblems(src, map[string]string{"Real": ""}); len(got) != 1 || !strings.Contains(got[0], "no reason") {
		t.Errorf("an entry with no reason: %v", got)
	}
	if got := allowlistProblems(src, map[string]string{"Real": "  "}); len(got) != 1 {
		t.Errorf("an entry with a blank reason: %v", got)
	}
	if got := allowlistProblems(src, map[string]string{"Gone": "was renamed"}); len(got) != 1 || !strings.Contains(got[0], "does not have") {
		t.Errorf("an entry naming a missing function: %v", got)
	}
}

// Fails if driver.go passes a bare session name to -t: every -t in it must
// take a pane id or a name made by sessionTarget or sessionScope. This is the
// shape of TestNoNewRouteIntoTheDaemonsOwnSocket: a scan of the source, so a
// new call site cannot bring the bare form back unseen.
func TestDriverNeverPassesABareSessionNameToDashT(t *testing.T) {
	src, err := os.ReadFile("driver.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, problem := range allowlistProblems(string(src), sessionTargetAllow) {
		t.Error(problem)
	}
	v, stats := guardScan(t, src, sessionTargetAllow)
	if len(v) != 0 {
		t.Errorf("driver.go passes something else to -t:\n%s", strings.Join(v, "\n"))
	}
	// The query, as run at this head: every -t in an argument list built by
	// d.cmd, append or a []string literal, by what follows it.
	t.Logf("-t sites: %d (sessionTarget %d, sessionScope %d, pane id %d)",
		stats.sites, stats.sessionTarget, stats.sessionScope, stats.paneID)
	if stats.sites == 0 || stats.sessionTarget == 0 || stats.sessionScope == 0 || stats.paneID == 0 {
		t.Fatalf("the scan found %+v, so it checked nothing of one kind", stats)
	}
	if stats.sites != stats.sessionTarget+stats.sessionScope+stats.paneID {
		t.Errorf("sites %+v: some -t took none of the three allowed forms", stats)
	}
}

// The guard is the only check behind two of the six sites (the bare
// kill-session and the bare NewSession set-option have no behavior test), so
// it is tried on planted sources: every form that gets a bare name to -t must
// be reported, and the forms the driver uses must not be.
func TestTheSessionTargetGuardCatchesWhatItShould(t *testing.T) {
	const prelude = `package tmux
func sessionTarget(n string) string { return "=" + n }
func sessionScope(n string) string { return "=" + n + ":" }
`
	wrap := func(body string) string {
		return prelude + "func (d *Driver) f(name, paneID string) {\n" + body + "\n}\n"
	}
	good := map[string]string{
		"sessionTarget":  `d.cmd("kill-session", "-t", sessionTarget(name))`,
		"sessionScope":   `args := []string{"new-window", "-t", sessionScope(name)}; _ = args`,
		"a pane id":      `d.cmd("capture-pane", "-t", paneID, "-p")`,
		"append pane id": `args = append(args, ";", "send-keys", "-t", paneID)`,
	}
	// The one function that makes a pane id may name it from new-window's output.
	if v := guardViolations(t, prelude+"func (d *Driver) NewPaneAt(session string) {\n"+
		"out, _ := d.cmd(\"new-window\", \"-t\", sessionScope(session), \"-P\").Output()\n"+
		"paneID := strings.TrimSpace(string(out))\n"+
		"d.cmd(\"select-pane\", \"-t\", paneID)\n}\n"); len(v) != 0 {
		t.Errorf("NewPaneAt's own pane id was reported: %v", v)
	}
	// The good forms run inside an entry point, where paneID is a pane id.
	wrapEntry := func(body string) string {
		return prelude + "func (d *Driver) PaneStatus(name, paneID string) {\n" + body + "\n}\n"
	}
	for name, body := range good {
		if v := guardViolations(t, wrapEntry(body)); len(v) != 0 {
			t.Errorf("%s was reported: %v", name, v)
		}
	}
	bad := map[string]string{
		"a plain variable":             `tgt := name; d.cmd("kill-session", "-t", tgt)`,
		"Sprintf":                      `d.cmd("kill-session", "-t", fmt.Sprintf("%s", name))`,
		"an empty-string add":          `d.cmd("kill-session", "-t", ""+name)`,
		"an index":                     `d.cmd("kill-session", "-t", names[0])`,
		"a split append":               `args = append(args, "kill-session"); args = append(args, "-t", name)`,
		"a local named paneID":         `{ paneID := name; d.cmd("kill-session", "-t", paneID) }`,
		"a reassigned paneID":          `paneID = name; d.cmd("kill-session", "-t", paneID)`,
		"a glued -t":                   `d.cmd("kill-session", "-t"+name)`,
		"a glued literal":              `d.cmd("has-session", "-ts")`,
		"a const flag":                 `const flagT = "-t"; d.cmd("kill-session", flagT, name)`,
		"a variable flag":              `flag := "-t"; d.cmd("kill-session", flag, name)`,
		"a split flag":                 `d.cmd("kill-session", "-"+"t", name)`,
		"a shadowed helper":            `sessionTarget := func(s string) string { return s }; d.cmd("kill-session", "-t", sessionTarget(name))`,
		"a glued Sprintf":              `d.cmd("kill-session", fmt.Sprintf("-t%s", name))`,
		"a conversion of -t":           `d.cmd("kill-session", string("-t"), name)`,
		"a flag built from a variable": `pre := "-"; d.cmd("kill-session", pre+"t", name)`,
		"an allowlisted name called with a session": `d.PaneStatus(name)`,
		"a flag cluster ending in t":                `d.cmd("kill-session", "-dt", name)`,
		"a flag cluster with t first":               `d.cmd("kill-session", "-td")`,
		"a flag cluster glued to a name":            `d.cmd("kill-session", "-dt"+name)`,
		"a cluster in a variable":                   `cl := "-d"; d.cmd("kill-session", cl+"t", name)`,
	}
	// A helper that takes a session name and puts it on -t is caught under any
	// parameter name, not just paneID.
	for _, param := range []string{"x", "id", "target", "sess"} {
		src := prelude + "func (d *Driver) killT(" + param + " string) { d.cmd(\"kill-session\", \"-t\", " + param + ") }\n" +
			"func (d *Driver) h(name string) { d.killT(name) }\n"
		if v := guardViolations(t, src); len(v) == 0 {
			t.Errorf("a helper taking its session name as %q was not reported", param)
		}
		src = prelude + "func sessArgs(" + param + " string) []string { return []string{\"-t\", " + param + "} }\n"
		if v := guardViolations(t, src); len(v) == 0 {
			t.Errorf("an args helper taking its session name as %q was not reported", param)
		}
	}
	// A cluster whose last flag is t takes the next argument as its target, so
	// that argument is checked like -t's.
	if v := guardViolations(t, wrap(`d.cmd("new-window", "-dt", sessionScope(name))`)); len(v) != 0 {
		t.Errorf("-dt with a sessionScope target was reported: %v", v)
	}
	// A parameter named paneID is a pane id only in the driver's own pane
	// entry points. A helper that takes the name and passes it to -t is how a
	// session name gets there, whatever the parameter is called.
	aliases := map[string]string{
		"a helper returning the args": "func sessArgs(paneID string) []string { return []string{\"-t\", paneID} }\n",
		"a helper appending the args": "func sessAppend(args []string, paneID string) []string { return append(args, \"-t\", paneID) }\n",
		"a method called with a name": "func (d *Driver) killT(paneID string) { d.cmd(\"kill-session\", \"-t\", paneID) }\nfunc (d *Driver) h(name string) { d.killT(name) }\n",
	}
	for name, src := range aliases {
		if v := guardViolations(t, prelude+src); len(v) == 0 {
			t.Errorf("%s was not reported", name)
		}
	}
	// The entry points themselves stay allowed.
	if v := guardViolations(t, prelude+"func (d *Driver) KillPane(paneID string) error { return d.cmd(\"kill-pane\", \"-t\", paneID).Run() }\n"); len(v) != 0 {
		t.Errorf("KillPane was reported: %v", v)
	}
	// A documented false positive is skipped when it is listed with a reason,
	// and only then.
	fp := wrap(`d.cmd("kill-session", "-t", "="+name)`)
	if v, _ := guardScan(t, fp, nil); len(v) == 0 {
		t.Error("the hand-built exact target was not reported without an allowlist entry")
	}
	if v, _ := guardScan(t, fp, map[string]string{"f": "builds the exact form by hand"}); len(v) != 0 {
		t.Errorf("an allowlisted function was reported: %v", v)
	}
	if v, _ := guardScan(t, fp, map[string]string{"f": ""}); len(v) == 0 {
		t.Error("an allowlist entry with no reason was honored")
	}
	// The pane id shape belongs to NewPaneAt alone: the same lines in another
	// function that has no paneID parameter are not a pane id.
	if v := guardViolations(t, prelude+"func (d *Driver) g(name string) {\n"+
		"out := []byte(name)\npaneID := strings.TrimSpace(string(out))\n"+
		"d.cmd(\"kill-session\", \"-t\", paneID)\n}\n"); len(v) == 0 {
		t.Error("the pane id shape was accepted outside NewPaneAt")
	}
	for name, body := range bad {
		if v := guardViolations(t, wrap(body)); len(v) == 0 {
			t.Errorf("%s was not reported: %s", name, body)
		}
	}
}
