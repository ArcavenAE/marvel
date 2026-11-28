// Package layering pins marvel's package layering in four structural rules.
// They are about who may import whom, not about how many imports a package
// has: a count would be a gate by accretion (diagnostic-not-gate), and every
// rule here is a yes or no on one import edge (aae-orc-75suc, I-797 S3).
package layering

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const module = "github.com/arcavenae/marvel/"

// listed runs go list over one package and returns the words it prints.
func listed(t *testing.T, format string, deps bool, pkg string) []string {
	t.Helper()
	args := []string{"list", "-f", format}
	if deps {
		args = []string{"list", "-deps", "-f", format}
	}
	out, err := exec.Command("go", append(args, module+pkg)...).Output()
	if err != nil {
		t.Fatalf("go list %s: %v", pkg, err)
	}
	return strings.Fields(string(out))
}

// marvelDeps is every marvel package the package depends on, itself excluded.
func marvelDeps(t *testing.T, pkg string) []string {
	t.Helper()
	var out []string
	for _, p := range listed(t, "{{if not .Standard}}{{.ImportPath}}{{end}}", true, pkg) {
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

// execImportersOutside returns each package in pkg's imports, other than
// allowed, that imports os/exec.
func execImportersOutside(t *testing.T, pkg, allowed string) []string {
	t.Helper()
	if pkg != allowed && slices.Contains(listed(t, "{{join .Imports \"\\n\"}}", false, pkg), "os/exec") {
		return []string{pkg}
	}
	return nil
}

// The stored model does not start processes; the one place it ran git for the
// trusted-folder check belongs in a leaf package.
func TestApiImportsNoOsExec(t *testing.T) {
	t.Parallel()
	for _, p := range execImportersOutside(t, "internal/api", "internal/hostprobe") {
		t.Errorf("%s imports os/exec: the stored model does not start processes", p)
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
