package daemon

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The one process the daemon package may start is the reexec handoff probe: it
// asks the binary the daemon is about to exec whether it can read the
// credential handoff (aae-orc-4h9xp, section 1.1). The architect's ruling lets
// the guard's own tool carry the exception: a named file, a pinned call and a
// pinned argument list, while every other file keeps the ban
// (marvel#556 routes W2 and W3 were closed the same way).

// execAllowedFile is the reexec probe's file. With attachExecFile it is one of
// the two non-test files allowed to import os/exec (director ruling
// 01M88CD904, item 1).
const execAllowedFile = "reexec_handoff.go"

// attachExecFile holds the attach channel's one process.
const attachExecFile = "attach_exec.go"

// execImportViolations lists every import in p that opens a route to a socket
// or a process, except os/exec in execAllowedFile.
func execImportViolations(p pkgFiles) []string {
	var out []string
	p.each(func(file string, n ast.Node) bool {
		imp, ok := n.(*ast.ImportSpec)
		if !ok {
			return true
		}
		path, _ := strconv.Unquote(imp.Path.Value)
		switch path {
		case "os/exec":
			if file != execAllowedFile && file != attachExecFile {
				out = append(out, file+" imports os/exec; only "+execAllowedFile+" and "+attachExecFile+" may")
			}
		case "net/http", "net/rpc":
			out = append(out, file+" imports "+path+"; the daemon package opens no other route to a socket or a process")
		}
		return true
	})
	return out
}

func TestOsExecIsAllowedInTheProbeFileAndNowhereElse(t *testing.T) {
	t.Parallel()
	if got := execImportViolations(parseDaemon(t)); len(got) != 0 {
		t.Errorf("the daemon package's own sources break the import guard: %v", got)
	}
}

// A planted import proves the ban still holds for every other file: the same
// source passes under the allowed name and fails under any other.
func TestAnOsExecImportInAnyOtherFileIsStillRefused(t *testing.T) {
	t.Parallel()
	const src = "package daemon\n\nimport \"os/exec\"\n\nvar _ = exec.Command\n"
	plant := func(name string) pkgFiles {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		return pkgFiles{fset: fset, files: map[string]*ast.File{name: f}}
	}
	// Two files may import os/exec: the reexec probe's, and the attach
	// command's (director ruling 01M88CD904, item 1). A name that only
	// resembles either is refused.
	for _, allowed := range []string{execAllowedFile, "attach_exec.go"} {
		if got := execImportViolations(plant(allowed)); len(got) != 0 {
			t.Errorf("the allowed file %s was refused: %v", allowed, got)
		}
	}
	for _, other := range []string{"daemon.go", "reexec_handoff_extra.go", "planted.go", "attach_exec_extra.go", "sshattach.go", "sshserver.go"} {
		if got := execImportViolations(plant(other)); len(got) != 1 || !strings.Contains(got[0], other) {
			t.Errorf("os/exec in %s: violations = %v, want one naming the file", other, got)
		}
	}
}

// The inventory: exactly two exec calls in the package, each in one function
// of one file, each with its arguments pinned. The probe's command builder
// takes the resolved exe and the three fixed arguments. The attach command
// builder takes the tmux binary, the socket name the resolver returned, and
// the desk, which the channel has already checked is digits only (director
// ruling 01M88CD904, item 2). Nothing else in the package starts a process.
func TestTheOnlyProcessesTheDaemonStartsAreTheProbeAndTheAttachClient(t *testing.T) {
	t.Parallel()
	const probeKey = "exec.CommandContext in probeCommand (reexec_handoff.go)"
	const attachKey = "exec.Command in attachCommand (attach_exec.go)"
	got := map[string]int{}
	args := map[string][]string{}
	p := parseDaemon(t)
	p.eachFunc(func(file, fun string, n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !isSel(sel, "exec", sel.Sel.Name) || !strings.HasPrefix(sel.Sel.Name, "Command") {
			return true
		}
		key := "exec." + sel.Sel.Name + " in " + fun + " (" + file + ")"
		got[key]++
		for _, a := range call.Args {
			// Quotes are kept: "tmux" the literal and tmux an identifier differ.
			args[key] = append(args[key], strings.Trim(exprText(a), " "))
		}
		return true
	})
	want := map[string]int{probeKey: 1, attachKey: 1}
	for k, n := range got {
		if want[k] != n {
			t.Errorf("%s: %d calls, inventoried as %d", k, n, want[k])
		}
	}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("inventoried %q: %d calls, now %d", k, n, got[k])
		}
	}
	wantArgs := map[string][]string{
		probeKey:  {"ctx", "exe", `"version"`, `"--capabilities"`, `"--json"`},
		attachKey: {`"tmux"`, `"-L"`, "socket", `"attach"`, `"-t"`, `"%"+desk`},
	}
	for k, w := range wantArgs {
		if strings.Join(args[k], ",") != strings.Join(w, ",") {
			t.Errorf("%s: call arguments are %v, pinned as %v", k, args[k], w)
		}
	}
}

// execCmdLiterals lists every exec.Cmd struct literal in p, in any function or
// at package level. The inventory above counts calls to exec.Command*, and an
// &exec.Cmd{Path: ...} builds a process without one (aae-orc-4h9xp review).
func execCmdLiterals(p pkgFiles) []string {
	var out []string
	p.each(func(file string, n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if ok && lit.Type != nil && isSel(lit.Type, "exec", "Cmd") {
			out = append(out, file+": exec.Cmd literal at "+p.fset.Position(lit.Pos()).String())
		}
		return true
	})
	return out
}

func TestAnExecCmdLiteralIsCountedLikeACall(t *testing.T) {
	t.Parallel()
	const src = "package daemon\n\nimport \"os/exec\"\n\nfunc probeCommand() *exec.Cmd { return &exec.Cmd{Path: \"x\"} }\n\nvar plain = exec.Cmd{}\n"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, execAllowedFile, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := execCmdLiterals(pkgFiles{fset: fset, files: map[string]*ast.File{execAllowedFile: f}})
	if len(got) != 2 {
		t.Errorf("exec.Cmd literals found = %v, want the pointer literal and the value literal", got)
	}
	if real := execCmdLiterals(parseDaemon(t)); len(real) != 0 {
		t.Errorf("the daemon package builds a process from a struct literal: %v", real)
	}
}

func exprText(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.BasicLit:
		return v.Value
	case *ast.BinaryExpr:
		return exprText(v.X) + v.Op.String() + exprText(v.Y)
	}
	return "?"
}

// The command itself, as the ruling pins it: argv0 is the exe the reexec
// already resolved, the environment is an empty slice and not nil (a nil Env
// would inherit the daemon's), stdin is nil, stdout is read through a small cap,
// and the timeout is two seconds.
func TestTheProbeCommandIsExactlyTheRuledShape(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	cmd := probeCommand(context.Background(), "/opt/marvel/marvel-target", &out)
	if cmd.Path != "/opt/marvel/marvel-target" {
		t.Errorf("argv0 = %q, want the resolved exe", cmd.Path)
	}
	if got := strings.Join(cmd.Args, " "); got != "/opt/marvel/marvel-target version --capabilities --json" {
		t.Errorf("args = %q", got)
	}
	if cmd.Env == nil || len(cmd.Env) != 0 {
		t.Errorf("Env = %#v, want an empty non-nil slice so the daemon's environment never reaches the target", cmd.Env)
	}
	if cmd.Stdin != nil {
		t.Errorf("Stdin = %v, want nil", cmd.Stdin)
	}
	lw, ok := cmd.Stdout.(*limitedWriter)
	if !ok || lw.left != 4096 {
		t.Errorf("Stdout = %#v, want a limitedWriter capped at 4096 bytes", cmd.Stdout)
	}
	if len(cmd.ExtraFiles) != 0 {
		t.Errorf("ExtraFiles = %v, want none: the probe runs before the pipe exists", cmd.ExtraFiles)
	}
	if handoffProbeTimeout != 2*time.Second {
		t.Errorf("probe timeout = %s, want 2s", handoffProbeTimeout)
	}
}
