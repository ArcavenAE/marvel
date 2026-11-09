package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// openCodeCtx is a launch of an opencode seat, headless or interactive, with the
// provider the role declares and the home the manager assigned.
func openCodeCtx(t *testing.T, mode api.RuntimeMode, provider, baseURL, home string) *LaunchContext {
	t.Helper()
	orig := opencodeProvider
	opencodeProvider = func(*LaunchContext) (string, string) { return provider, baseURL }
	t.Cleanup(func() { opencodeProvider = orig })
	ctx := testContext()
	ctx.Session.Runtime = api.Runtime{Name: "opencode", Command: "opencode", Mode: mode, Prompt: "do the thing"}
	ctx.Role.Runtime = ctx.Session.Runtime
	ctx.HarnessHomePath = home
	return ctx
}

func TestOpenCodeIsSessionHomeAssigner(t *testing.T) {
	a, ok := Adapter(&OpenCode{}).(SessionHomeAssigner)
	if !ok {
		t.Fatal("opencode is not a SessionHomeAssigner")
	}
	// A role that declares no provider, which is every role today, shares the home.
	if _, want := a.SessionHome(openCodeCtx(t, api.RuntimeModeHeadless, "", "", "")); want {
		t.Error("a role with no declared provider was given a private home")
	}
	spec, want := a.SessionHome(openCodeCtx(t, api.RuntimeModeHeadless, "ollama", "http://127.0.0.1:11434", ""))
	if !want || spec.EnvVar != "XDG_DATA_HOME" {
		t.Fatalf("SessionHome = %+v, %v; want a private home on XDG_DATA_HOME", spec, want)
	}
	if len(spec.LinkIn) != 0 || spec.Source != "" {
		t.Errorf("the private home links or seeds from the operator's home: %+v", spec)
	}
}

// Price is never the test: only a declared lmstudio or ollama with a loopback
// base URL gets a private home. The off-host case is the control for the
// loopback ones.
func TestOpenCodePrivateHomeGoesOnlyToALoopbackLocalProvider(t *testing.T) {
	cases := []struct {
		name, provider, url string
		private             bool
	}{
		{"ollama on 127.0.0.1", "ollama", "http://127.0.0.1:11434", true},
		{"ollama on localhost", "ollama", "http://localhost:11434/v1", true},
		{"ollama on ::1", "ollama", "http://[::1]:11434", true},
		{"lmstudio on loopback", "lmstudio", "http://127.0.0.1:1234/v1", true},
		{"ollama off the host", "ollama", "http://192.168.1.20:11434", false},
		{"ollama on a name", "ollama", "http://ollama.example.com:11434", false},
		{"lmstudio off the host", "lmstudio", "http://10.0.0.5:1234/v1", false},
		{"a loopback name that is a user part", "ollama", "http://127.0.0.1@evil.example/v1", false},
		{"a name that starts with localhost", "ollama", "http://localhost.evil.example", false},
		{"no base URL", "ollama", "", false},
		{"the opencode provider on loopback", "opencode", "http://127.0.0.1:1234", false},
		{"opencode with no URL", "opencode", "", false},
		{"another provider on loopback", "anthropic", "http://localhost:8080", false},
		{"provider case differs", "Ollama", "http://localhost:11434", false},
		{"host case differs", "ollama", "http://LOCALHOST:11434", false},
		{"host case differs on lmstudio", "lmstudio", "http://Localhost:1234/v1", false},
		{"scheme is not http", "ollama", "ftp://127.0.0.1:11434", false},
		{"scheme is a file", "lmstudio", "file://localhost/models", false},
		{"no scheme", "ollama", "127.0.0.1:11434", false},
		{"scheme case is http", "ollama", "HTTP://127.0.0.1:11434", true},
		{"no provider", "", "", false},
		{"no provider, loopback URL", "", "http://127.0.0.1", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, got := (&OpenCode{}).SessionHome(openCodeCtx(t, api.RuntimeModeHeadless, c.provider, c.url, ""))
			if got != c.private {
				t.Fatalf("private home for %q at %q = %v, want %v", c.provider, c.url, got, c.private)
			}
		})
	}
}

// C4: a headless launch that is to have a private home and has none is refused
// by a stub that exits 78 and returns a command, not a Prepare error.
func TestOpenCodePrepareHeadlessNoHomeRefuses(t *testing.T) {
	for name, home := range map[string]string{"empty": "", "relative": "rel/home"} {
		t.Run(name, func(t *testing.T) {
			ctx := openCodeCtx(t, api.RuntimeModeHeadless, "ollama", "http://127.0.0.1:11434", home)
			res, err := (&OpenCode{}).Prepare(ctx)
			if err != nil || res == nil {
				t.Fatalf("Prepare = %v, %v; want a stub command and no error", res, err)
			}
			if !strings.Contains(res.Command, "exit 78") || !strings.Contains(res.Command, "no-private-home") {
				t.Errorf("command = %q, want the exit-78 no-private-home stub", res.Command)
			}
			if strings.Contains(res.Command, "'run' '--format' 'json'") {
				t.Errorf("the refused launch still runs opencode: %q", res.Command)
			}
			if res.Stream != nil {
				t.Error("a refused launch carries a stream")
			}
		})
	}
	// Controls: with an absolute home the same seat launches; a seat that shares
	// the operator's home launches with no home, as it does today; and an
	// interactive seat with no home is not refused.
	ok := openCodeCtx(t, api.RuntimeModeHeadless, "ollama", "http://127.0.0.1:11434", t.TempDir())
	if res, err := (&OpenCode{}).Prepare(ok); err != nil || !strings.Contains(res.Command, "'run' '--format' 'json'") {
		t.Fatalf("a private-home seat with a home must launch: %v %+v", err, res)
	}
	shared := openCodeCtx(t, api.RuntimeModeHeadless, "", "", "")
	if res, err := (&OpenCode{}).Prepare(shared); err != nil || !strings.Contains(res.Command, "'run' '--format' 'json'") {
		t.Fatalf("a shared-home seat must launch as today: %v %+v", err, res)
	}
	inter := openCodeCtx(t, api.RuntimeModeInteractive, "ollama", "http://127.0.0.1:11434", "")
	if res, err := (&OpenCode{}).Prepare(inter); err != nil || strings.Contains(res.Command, "exit 78") {
		t.Fatalf("an interactive seat is not refused: %v %+v", err, res)
	}
}

// V4 (b): the data, cache and state homes move and the config home does not.
func TestOpenCodePrepareLeavesConfigHome(t *testing.T) {
	home := t.TempDir()
	ctx := openCodeCtx(t, api.RuntimeModeHeadless, "ollama", "http://localhost:11434", home)
	res, err := (&OpenCode{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got := res.Env["XDG_DATA_HOME"]; got != filepath.Join(home, "data") {
		t.Fatalf("XDG_DATA_HOME = %q, want %q", got, filepath.Join(home, "data"))
	}
	if got := res.Env["XDG_CACHE_HOME"]; got != filepath.Join(home, "cache") {
		t.Errorf("XDG_CACHE_HOME = %q, want %q", got, filepath.Join(home, "cache"))
	}
	if got := res.Env["XDG_STATE_HOME"]; got != filepath.Join(home, "state") {
		t.Errorf("XDG_STATE_HOME = %q, want %q", got, filepath.Join(home, "state"))
	}
	if v, present := res.Env["XDG_CONFIG_HOME"]; present {
		t.Errorf("XDG_CONFIG_HOME = %q is set; the operator's config home must stay", v)
	}
	if OpenCodeCacheHome(home) != filepath.Join(home, "cache") {
		t.Errorf("OpenCodeCacheHome(%q) = %q", home, OpenCodeCacheHome(home))
	}
	// A seat that shares the home sets none of the three.
	shared, err := (&OpenCode{}).Prepare(openCodeCtx(t, api.RuntimeModeHeadless, "", "", ""))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_CONFIG_HOME"} {
		if _, present := shared.Env[k]; present {
			t.Errorf("a shared-home seat sets %s", k)
		}
	}
}

// The seeder makes the three directories, private to the user.
func TestOpenCodeSeedMakesThePrivateHomeDirectories(t *testing.T) {
	spec, want := (&OpenCode{}).SessionHome(openCodeCtx(t, api.RuntimeModeHeadless, "lmstudio", "http://127.0.0.1:1234", ""))
	if !want || spec.Seed == nil {
		t.Fatalf("spec = %+v, want a private home with a seeder", spec)
	}
	home := t.TempDir()
	if err := spec.Seed(home); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	for _, d := range []string{"data", "cache", "state"} {
		fi, err := os.Stat(filepath.Join(home, d))
		if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v, %v; want a 0700 directory", d, fi, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "config")); err == nil {
		t.Error("the seeder made a config directory")
	}
}
