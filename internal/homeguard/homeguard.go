// Package homeguard keeps a package's tests out of the operator's real home.
//
// Every package with tests calls Run from its TestMain. Run gives the test
// binary a scratch HOME and no MARVEL_* variables, so a test that forgets
// t.Setenv resolves ~/.marvel, ~/.codex and ~/.claude.json inside a temp dir.
// After the run it checks that the paths marvel resolves still land under the
// one expected root, and fails the binary if a test repointed HOME or left a
// MARVEL_* variable set.
//
// ExpectedRoot is the single place that says where marvel's layout lives under
// a home. If marvel later runs more than one cluster per host, that function
// changes here and no package's tests do.
package homeguard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/paths"
)

// Run is the stub the red commit carries: it runs the tests as they are.
func Run(m *testing.M) int { return m.Run() }

// Check is the stub the red commit carries: it reports nothing.
func Check(home string, want paths.Layout) []string { return nil }

func under(path, root string) bool { return true }

var _ = []any{fmt.Sprint, os.Getenv, strings.HasPrefix, filepath.Join}
