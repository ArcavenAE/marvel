// Package layering pins marvel's package layering in four structural rules.
// They are about who may import whom, not about how many imports a package
// has: a count would be a gate by accretion (diagnostic-not-gate), and every
// rule here is a yes or no on which package may reach which (aae-orc-75suc,
// I-797 S3).
package layering

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const module = "github.com/arcavenae/marvel/"

// listed runs go list over one package and everything it depends on, and returns
// the words it prints.
func listed(t *testing.T, format, pkg string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", "-f", format, module+pkg).Output()
	if err != nil {
		t.Fatalf("go list %s: %v", pkg, err)
	}
	return strings.Fields(string(out))
}

// marvelDeps is every marvel package the package depends on, itself excluded.
func marvelDeps(t *testing.T, pkg string) []string {
	t.Helper()
	var out []string
	for _, p := range listed(t, "{{if not .Standard}}{{.ImportPath}}{{end}}", pkg) {
		if strings.HasPrefix(p, module) && p != module+pkg {
			out = append(out, strings.TrimPrefix(p, module))
		}
	}
	return out
}

// forbid reports each dependency of pkg that the match accepts.
func forbid(t *testing.T, pkg string, why string, match func(dep string) bool) {
	t.Helper()
	for _, d := range marvelDeps(t, pkg) {
		if match(d) {
			t.Errorf("%s depends on %s: %s", pkg, d, why)
		}
	}
}

// The daemon is a library the commands call, never the other way round.
func TestDaemonImportsNoCmd(t *testing.T) {
	t.Parallel()
	forbid(t, "internal/daemon", "a command package is above the daemon", func(d string) bool {
		return strings.HasPrefix(d, "cmd/")
	})
}

// api, tmux, session and team sit below the daemon, which wires them together.
func TestLowerLayersImportNoDaemon(t *testing.T) {
	t.Parallel()
	for _, pkg := range []string{"internal/api", "internal/tmux", "internal/session", "internal/team"} {
		t.Run(pkg, func(t *testing.T) {
			t.Parallel()
			forbid(t, pkg, "the daemon is above it", func(d string) bool { return d == "internal/daemon" })
		})
	}
}

// tmux is the driver for one subcommand at a time and needs no stored model.
func TestTmuxImportsNoApi(t *testing.T) {
	t.Parallel()
	forbid(t, "internal/tmux", "the tmux driver is below the stored model", func(d string) bool {
		return d == "internal/api"
	})
}

// importsOsExec prints a package's path when the package itself imports
// os/exec, and nothing otherwise.
const importsOsExec = `{{$p := .ImportPath}}{{range .Imports}}{{if eq . "os/exec"}}{{$p}}{{end}}{{end}}`

// execImportersOutside returns, sorted, every package that imports os/exec among
// pkg and all it depends on, allowed excepted. It reads the build go list sees
// where the test runs, without test files.
func execImportersOutside(t *testing.T, pkg, allowed string) []string {
	t.Helper()
	var out []string
	for _, p := range listed(t, importsOsExec, pkg) {
		if p = strings.TrimPrefix(p, module); p != allowed {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// The stored model reaches os/exec through one leaf package, internal/hostprobe,
// which holds the git calls and the PATH lookup the model needs. No other
// package api depends on, api itself included, may import os/exec.
func TestApiReachesOsExecOnlyThroughHostprobe(t *testing.T) {
	t.Parallel()
	for _, p := range execImportersOutside(t, "internal/api", "internal/hostprobe") {
		t.Errorf("%s imports os/exec under internal/api: only internal/hostprobe may", p)
	}
}

// Rule 4 has to see a second importer of os/exec anywhere under the package,
// not only an import in the package itself. The fixture reaches one through the
// allowed package and one two imports down.
func TestExecRuleReportsASecondImporterUnderThePackage(t *testing.T) {
	t.Parallel()
	const fixture = "internal/layering/testdata/execrule/"
	got := execImportersOutside(t, fixture+"root", fixture+"allowed")
	if want := []string{fixture + "second"}; !slices.Equal(got, want) {
		t.Errorf("os/exec importers under the fixture outside the allowed package = %v, want %v", got, want)
	}
}
