package usage

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/runtime/claudecode"
	"github.com/arcavenae/marvel/internal/runtime/opencode"
)

const opencodeFixture = "testdata/opencode-models.json"

func TestOpenCodeWindow(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	malformed := filepath.Join(dir, "malformed.json")
	if err := os.WriteFile(malformed, []byte(`{"opencode": {"models": {"x": `), 0o600); err != nil {
		t.Fatal(err)
	}
	wrongShape := filepath.Join(dir, "wrongshape.json")
	if err := os.WriteFile(wrongShape, []byte(`["not", "an", "object"]`), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		path    string
		model   string
		want    int
		wantErr bool
	}{
		{name: "hit", path: opencodeFixture, model: "opencode/claude-haiku-4-5", want: 200_000},
		{name: "model id with a slash splits at the first one", path: opencodeFixture, model: "openrouter/anthropic/claude-sonnet-4", want: 1_000_000},
		{name: "model missing from the file", path: opencodeFixture, model: "opencode/not-a-model", wantErr: true},
		{name: "provider missing from the file", path: opencodeFixture, model: "nobody/claude-haiku-4-5", wantErr: true},
		{name: "no provider part", path: opencodeFixture, model: "claude-haiku-4-5", wantErr: true},
		{name: "empty model", path: opencodeFixture, model: "", wantErr: true},
		{name: "model without a limit", path: opencodeFixture, model: "opencode/no-limit-model", wantErr: true},
		{name: "zero limit is a refusal, never a window of 0", path: opencodeFixture, model: "opencode/zero-limit-model", wantErr: true},
		{name: "fractional limit is not a token count", path: opencodeFixture, model: "opencode/fractional-model", wantErr: true},
		{name: "missing file", path: filepath.Join(dir, "absent.json"), model: "opencode/claude-haiku-4-5", wantErr: true},
		{name: "malformed file", path: malformed, model: "opencode/claude-haiku-4-5", wantErr: true},
		{name: "wrong top-level shape", path: wrongShape, model: "opencode/claude-haiku-4-5", wantErr: true},
		{name: "empty path", path: "", model: "opencode/claude-haiku-4-5", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := OpenCodeWindow(tt.path, tt.model)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("OpenCodeWindow = %d, nil; want an error", got)
				}
				if got != 0 {
					t.Errorf("OpenCodeWindow returned %d with an error, want 0", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("OpenCodeWindow error: %v", err)
			}
			if got != tt.want {
				t.Errorf("OpenCodeWindow = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestOpenCodeWindowRefusesAPathThatIsNotAPlainCatalogFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const model = "opencode/claude-haiku-4-5"

	fifo := filepath.Join(dir, "fifo.json")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	link := filepath.Join(dir, "link.json")
	abs, err := filepath.Abs(opencodeFixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(abs, link); err != nil {
		t.Fatal(err)
	}
	huge := filepath.Join(dir, "huge.json")
	// A valid catalog padded with blanks past the cap: without the cap it
	// parses and returns 200000, so only the cap makes this a refusal.
	good, err := os.ReadFile(opencodeFixture)
	if err != nil {
		t.Fatal(err)
	}
	padded := append(good, bytes.Repeat([]byte(" "), maxOpenCodeCatalogBytes+1-len(good))...)
	if err := os.WriteFile(huge, padded, 0o600); err != nil {
		t.Fatal(err)
	}
	pastInt32 := filepath.Join(dir, "int32.json")
	if err := os.WriteFile(pastInt32, []byte(`{"opencode":{"models":{"claude-haiku-4-5":{"limit":{"context":3000000000}}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, path := range map[string]string{
		"a FIFO":                  fifo,
		"a symlink to a real one": link,
		"a file over the cap":     huge,
		"a limit past int32":      pastInt32,
		"a directory":             dir,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			done := make(chan struct{})
			var got int
			var gotErr error
			go func() {
				defer close(done)
				got, gotErr = OpenCodeWindow(path, model)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("OpenCodeWindow blocked; a FIFO at the catalog path must be refused without opening it")
			}
			if gotErr == nil || got != 0 {
				t.Errorf("OpenCodeWindow = %d, %v; want 0 and an error", got, gotErr)
			}
		})
	}
}

func TestOpenCodeModelsPath(t *testing.T) {
	t.Parallel()

	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	tests := []struct {
		name string
		env  map[string]string
		home string
		want string
	}{
		{name: "XDG_CACHE_HOME wins", env: map[string]string{"XDG_CACHE_HOME": "/x/cache"}, home: "/h", want: "/x/cache/opencode/models.json"},
		{name: "home cache otherwise", env: nil, home: "/h", want: "/h/.cache/opencode/models.json"},
		{name: "a relative XDG_CACHE_HOME is ignored, as xdg-basedir does", env: map[string]string{"XDG_CACHE_HOME": "rel/cache"}, home: "/h", want: "/h/.cache/opencode/models.json"},
		{name: "no home and no XDG leaves no path", env: nil, home: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := OpenCodeModelsPath(env(tt.env), tt.home); got != tt.want {
				t.Errorf("OpenCodeModelsPath = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOpenCodeWindowErrorsAreNotFoundOrUnreadable(t *testing.T) {
	t.Parallel()
	_, err := OpenCodeWindow(filepath.Join(t.TempDir(), "absent.json"), "opencode/claude-haiku-4-5")
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing file should wrap os.ErrNotExist so a caller can tell it from a bad file; got %v", err)
	}
}

// The tests below drive the Accountant, which is where the catalog is read
// (once, at Bind: Observe may not do file I/O).

func bindOpenCode(t *testing.T, a *Accountant, args []string, window int) {
	t.Helper()
	a.Bind(testCoords, Bind{Harness: opencode.Harness, Args: args, Window: window, Redirection: api.BackendDefault})
}

func TestBindReadsOpenCodeCatalogWhenNoManifestWindow(t *testing.T) {
	t.Parallel()
	a, sink, _ := newTestAccountant(t, Table{})
	a.opencodeModelsPath = opencodeFixture

	bindOpenCode(t, a, []string{"-m", "opencode/claude-haiku-4-5"}, 0)
	a.Observe(testCoords, turnEvent(opencode.Harness, additive(20_000, 0, 0)))

	got, ok := sink.get(testCoords.AgentID)
	if !ok {
		t.Fatal("no reading written")
	}
	if got.ContextLimit != 200_000 {
		t.Errorf("limit = %d, want 200000 from the catalog", got.ContextLimit)
	}
	if got.ContextLimitSource != string(LimitFromFeed) {
		t.Errorf("limit source = %q, want %q", got.ContextLimitSource, LimitFromFeed)
	}
	if got.ContextPercent < 9.9 || got.ContextPercent > 10.1 {
		t.Errorf("percent = %v, want about 10", got.ContextPercent)
	}
}

func TestOpenCodeManifestWindowBeatsTheCatalog(t *testing.T) {
	t.Parallel()
	a, sink, _ := newTestAccountant(t, Table{})
	a.opencodeModelsPath = opencodeFixture

	bindOpenCode(t, a, []string{"-m", "opencode/claude-haiku-4-5"}, 64_000)
	a.Observe(testCoords, turnEvent(opencode.Harness, additive(6_400, 0, 0)))

	got, _ := sink.get(testCoords.AgentID)
	if got.ContextLimit != 64_000 || got.ContextLimitSource != string(LimitFromManifest) {
		t.Errorf("limit = %d from %q, want 64000 from %q", got.ContextLimit, got.ContextLimitSource, LimitFromManifest)
	}
}

func TestOpenCodeCatalogFailureLeavesTheWindowUnresolved(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"missing file":   filepath.Join(dir, "absent.json"),
		"malformed file": bad,
		"no path":        "",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			a, sink, _ := newTestAccountant(t, Table{})
			a.opencodeModelsPath = path

			bindOpenCode(t, a, []string{"-m", "opencode/claude-haiku-4-5"}, 0)
			a.Observe(testCoords, turnEvent(opencode.Harness, additive(20_000, 0, 0)))

			got, ok := sink.get(testCoords.AgentID)
			if !ok {
				t.Fatal("no reading written")
			}
			if got.ContextLimit != 0 || got.ContextLimitSource != string(LimitUnresolved) {
				t.Errorf("limit = %d from %q, want 0 from %q", got.ContextLimit, got.ContextLimitSource, LimitUnresolved)
			}
		})
	}
}

// A claude seat resolves nothing from an empty table and writes a reading
// with no window, so a catalog hit on it is visible. Without the opencode
// guard in catalogLimit this test fails: the seat would take 200000 from the
// catalog through its -m argument. (A codex seat writes no reading when its
// window is unresolved, so it cannot show this.)
func TestOpenCodeCatalogIsNotReadForOtherHarnesses(t *testing.T) {
	t.Parallel()
	a, sink, _ := newTestAccountant(t, Table{})
	a.opencodeModelsPath = opencodeFixture

	a.Bind(testCoords, Bind{Harness: claudecode.Harness, Args: []string{"-m", "opencode/claude-haiku-4-5"}, Redirection: api.BackendDefault})
	a.Observe(testCoords, turnEvent(claudecode.Harness, additive(20_000, 0, 0)))

	got, ok := sink.get(testCoords.AgentID)
	if !ok {
		t.Fatal("a claude seat wrote no reading, so this test cannot see a catalog hit; fix the test")
	}
	if got.ContextLimit != 0 || got.ContextLimitSource != string(LimitUnresolved) {
		t.Errorf("a claude seat took limit %d from %q; the opencode catalog is for opencode seats only", got.ContextLimit, got.ContextLimitSource)
	}
}
