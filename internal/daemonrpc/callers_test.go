package daemonrpc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const daemonImport = "github.com/arcavenae/marvel/internal/daemon"

// moved is every name the move took out of internal/daemon. A caller outside
// that package says daemonrpc.<name>; it does not reach the name through
// daemon, where only aliases and forwarders for them remain during the move
// (docs/design/code-restructure-plan.md, Move 1).
var moved = map[string]bool{
	"Request": true, "Response": true, "EventsBatch": true, "DialOptions": true,
	"MethodVersion": true, "MethodEventsWatch": true, "ErrWatchUnsupported": true,
	"SendRequest": true, "SendRequestWith": true, "WatchEventsWith": true,
	"DefaultMRVLPort": true, "ListenNetwork": true, "IsMRVL": true, "IsSSH": true,
}

// movedThroughDaemon returns each selector in src that names a moved identifier
// through the file's import of internal/daemon, whatever the import is called.
func movedThroughDaemon(t *testing.T, filename string, src any) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	local := ""
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		if path != daemonImport {
			continue
		}
		local = "daemon"
		if imp.Name != nil {
			local = imp.Name.Name
		}
	}
	if local == "" || local == "_" || local == "." {
		return nil
	}
	var hits []string
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == local && moved[sel.Sel.Name] {
			hits = append(hits, sel.Sel.Name)
		}
		return true
	})
	return hits
}

func TestTheDetectorSeesAMovedNameThroughTheDaemonImport(t *testing.T) {
	const src = `package x
import d "` + daemonImport + `"
var _ = d.Request{}
var _ d.Response
var _ = d.NewWithOptions
`
	got := movedThroughDaemon(t, "x.go", src)
	if strings.Join(got, ",") != "Request,Response" {
		t.Errorf("detector found %v, want [Request Response]", got)
	}
	if got := movedThroughDaemon(t, "y.go", "package y\nimport \"fmt\"\nvar _ = fmt.Sprint\n"); len(got) != 0 {
		t.Errorf("a file without the daemon import has hits: %v", got)
	}
}

// After PR 1b no file outside internal/daemon reaches a moved name through the
// daemon import. The aliases stay in internal/daemon until PR 1c, so a file that
// 1b missed would still build; this is the check that none was missed.
func TestNoCallerOutsideTheDaemonNamesAMovedIdentifierThroughIt(t *testing.T) {
	root := "../.."
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor":
				return filepath.SkipDir
			}
			if filepath.ToSlash(path) == root+"/internal/daemon" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if _, err := os.Stat(path); err != nil {
			return err
		}
		if hits := movedThroughDaemon(t, path, nil); len(hits) > 0 {
			offenders = append(offenders, filepath.ToSlash(strings.TrimPrefix(path, root+"/"))+": "+strings.Join(uniq(hits), " "))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Errorf("%d file(s) outside internal/daemon name a moved identifier through the daemon import:\n  %s", len(offenders), strings.Join(offenders, "\n  "))
	}
}

func uniq(s []string) []string {
	sort.Strings(s)
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}
