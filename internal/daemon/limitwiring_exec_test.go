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

// execAllowedFile is the one non-test file allowed to import os/exec.
const execAllowedFile = "reexec_handoff.go"

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
			if file != execAllowedFile {
				out = append(out, file+" imports os/exec; only "+execAllowedFile+" may, for the reexec handoff probe")
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
	if got := execImportViolations(plant(execAllowedFile)); len(got) != 0 {
		t.Errorf("the allowed file was refused: %v", got)
	}
	for _, other := range []string{"daemon.go", "reexec_handoff_extra.go", "planted.go"} {
		if got := execImportViolations(plant(other)); len(got) != 1 || !strings.Contains(got[0], other) {
			t.Errorf("os/exec in %s: violations = %v, want one naming the file", other, got)
		}
	}
}

// The inventory: exactly one exec call in the package, in the probe's command
// builder, with argv0 the resolved exe and the three fixed arguments.
func TestTheOnlyProcessTheDaemonStartsIsTheProbeWithItsPinnedArguments(t *testing.T) {
	t.Parallel()
	got := map[string]int{}
	var args []string
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
		got["exec."+sel.Sel.Name+" in "+fun+" ("+file+")"]++
		for _, a := range call.Args {
			args = append(args, strings.Trim(strings.ReplaceAll(exprText(a), `"`, ""), " "))
		}
		return true
	})
	want := map[string]int{"exec.CommandContext in probeCommand (reexec_handoff.go)": 1}
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
	if wantArgs := []string{"ctx", "exe", "version", "--capabilities", "--json"}; strings.Join(args, ",") != strings.Join(wantArgs, ",") {
		t.Errorf("the probe's call arguments are %v, pinned as %v", args, wantArgs)
	}
}

func exprText(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.BasicLit:
		return v.Value
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
