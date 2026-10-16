package usage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
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

// The tests below drive the Accountant, which is where the database is read
// (once, at Bind: Observe may not do file I/O).

func bindOpenCode(t *testing.T, a *Accountant, args []string, window int) {
	t.Helper()
	a.Bind(testCoords, Bind{Harness: opencode.Harness, Args: args, Window: window, Redirection: api.BackendDefault})
}

func TestBindReadsOpenCodeDatabaseWhenNoManifestWindow(t *testing.T) {
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
		t.Errorf("limit = %d, want 200000 from the database", got.ContextLimit)
	}
	if got.ContextLimitSource != string(LimitFromFeed) {
		t.Errorf("limit source = %q, want %q", got.ContextLimitSource, LimitFromFeed)
	}
	if got.ContextPercent < 9.9 || got.ContextPercent > 10.1 {
		t.Errorf("percent = %v, want about 10", got.ContextPercent)
	}
}

func TestOpenCodeManifestWindowBeatsTheDatabase(t *testing.T) {
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

func TestOpenCodeDatabaseFailureLeavesTheWindowUnresolved(t *testing.T) {
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

func TestOpenCodeDatabaseIsNotReadForOtherHarnesses(t *testing.T) {
	t.Parallel()
	a, sink, _ := newTestAccountant(t, Table{})
	a.opencodeModelsPath = opencodeFixture

	a.Bind(testCoords, Bind{Harness: "codex", Args: []string{"-m", "opencode/claude-haiku-4-5"}, Redirection: api.BackendDefault})
	a.Observe(testCoords, turnEvent("codex", additive(20_000, 0, 0)))

	if got, ok := sink.get(testCoords.AgentID); ok && got.ContextLimit != 0 {
		t.Errorf("a codex session took limit %d from the opencode database", got.ContextLimit)
	}
}
