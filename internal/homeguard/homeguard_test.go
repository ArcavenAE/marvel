package homeguard

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) { os.Exit(Run(m)) }

func TestRunGivesAScratchHomeAndNoMarvelEnv(t *testing.T) {
	home := os.Getenv("HOME")
	if !strings.HasPrefix(home, filepath.Join(os.TempDir(), "marvel-test-home-")) {
		t.Errorf("HOME = %q, want a marvel-test-home- dir under the temp dir", home)
	}
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "MARVEL_") {
			t.Errorf("%s is set inside a guarded test binary", name)
		}
	}
}

func TestCheckAcceptsTheScratchHome(t *testing.T) {
	home := os.Getenv("HOME")
	if problems := Check(home); len(problems) != 0 {
		t.Errorf("Check on a clean scratch home reported %v", problems)
	}
}

func TestCheckFlagsAMovedHome(t *testing.T) {
	scratch := os.Getenv("HOME")
	t.Setenv("HOME", t.TempDir())
	if problems := Check(scratch); len(problems) == 0 {
		t.Error("Check accepted a HOME that moved off the scratch home")
	}
}

func TestCheckFlagsALeftoverMarvelVariable(t *testing.T) {
	t.Setenv("MARVEL_SOCKET", "/tmp/x.sock")
	problems := Check(os.Getenv("HOME"))
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "MARVEL_SOCKET") {
		t.Errorf("Check did not name the leftover variable: %v", problems)
	}
}

// The expected root is the one parameter: a root that does not match where
// paths resolves is reported, so changing the layout changes this one place.
func TestCheckComparesAgainstTheExpectedRoot(t *testing.T) {
	old := ExpectedRoot
	t.Cleanup(func() { ExpectedRoot = old })
	ExpectedRoot = func(home string) string { return filepath.Join(home, "elsewhere") }
	if problems := Check(os.Getenv("HOME")); len(problems) == 0 {
		t.Error("Check accepted paths outside the expected root")
	}
}

// TestEveryPackageWithTestsHasTheGuard lists every directory that holds a
// _test.go file and fails for each whose tests never call homeguard.Run.
func TestEveryPackageWithTestsHasTheGuard(t *testing.T) {
	root := moduleRoot(t)
	var missing []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		switch name := d.Name(); {
		case path == root:
		case name == "testdata", name == "vendor", strings.HasPrefix(name, "."), strings.HasPrefix(name, "_"):
			return filepath.SkipDir
		}
		tests, _ := filepath.Glob(filepath.Join(path, "*_test.go"))
		if len(tests) == 0 {
			return nil
		}
		for _, f := range tests {
			b, err := os.ReadFile(f)
			if err != nil {
				return err
			}
			if strings.Contains(string(b), "homeguard.Run(") || (filepath.Base(path) == "homeguard" && strings.Contains(string(b), "Run(m)")) {
				return nil
			}
		}
		rel, _ := filepath.Rel(root, path)
		missing = append(missing, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) > 0 {
		t.Errorf("%d package(s) with tests do not call homeguard.Run from TestMain:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test directory")
		}
		dir = parent
	}
}
