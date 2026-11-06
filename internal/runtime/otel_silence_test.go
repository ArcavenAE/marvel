package runtime

import (
	"regexp"
	"sort"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// aae-orc-0uhdm, I-100 P1: marvel sends no telemetry unless a user configured
// it. A seat marvel launches never carries an OTEL_EXPORTER_* variable that
// marvel put there, for any harness, so no harness starts exporting because of
// marvel. The three that read the stamped OTEL_RESOURCE_ATTRIBUTES (and goose,
// which sets OTEL_SDK_DISABLED) are in the matrix on purpose: they are the
// builders that already touch OTEL_*.

var otelExporterVar = regexp.MustCompile(`^OTEL_EXPORTER_`)

// exporterKeys returns the OTEL_EXPORTER_* keys of env, sorted. Only keys are
// returned: a value can be an endpoint or a header, and a failure message must
// not carry one.
func exporterKeys(env map[string]string) []string {
	var keys []string
	for k := range env {
		if otelExporterVar.MatchString(k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// launchEnvs builds the launch env of every harness marvel has a builder for,
// each with no telemetry env declared on its role, interactive and headless
// where the harness has both.
func launchEnvs(t *testing.T) map[string]map[string]string {
	t.Helper()
	envs := map[string]map[string]string{}
	prep := func(name string, a Adapter, ctx *LaunchContext) {
		t.Helper()
		r, err := a.Prepare(ctx)
		if err != nil {
			t.Fatalf("%s: Prepare: %v", name, err)
		}
		if len(r.Env) == 0 {
			t.Fatalf("%s: Prepare returned no env, so the check below would pass on nothing", name)
		}
		envs[name] = r.Env
	}
	prep("claude interactive", &Claude{}, claudeCtx("claude"))
	prep("claude headless", &Claude{}, claudePromptCtx("claude", api.RuntimeModeHeadless, "say ok", nil))
	prep("codex interactive", &Codex{}, codexCtx(api.RuntimeModeInteractive, ""))
	prep("codex headless", &Codex{}, codexCtx(api.RuntimeModeHeadless, "say ok"))
	prep("goose interactive", &Goose{}, gooseContext(t, nil))
	prep("goose headless", &Goose{}, gooseContext(t, headless))
	gen := testContext()
	gen.Session.Runtime.Name = "generic"
	gen.Session.Runtime.Command = "/bin/true"
	gen.Session.Runtime.Args = nil
	prep("generic", &Generic{}, gen)
	return envs
}

// A role that declares no telemetry env gets a seat with no OTEL_EXPORTER_*
// variable from marvel.
func TestUnsetKeyLaunchedSeatHasNoOTELExporterVar(t *testing.T) {
	t.Setenv(resourceAttrs, "")
	for name, env := range launchEnvs(t) {
		if keys := exporterKeys(env); len(keys) > 0 {
			t.Errorf("%s: marvel put %v in the seat's env", name, keys)
		}
	}
}

// A variable the daemon's own process carries is not marvel's doing: none of
// the builders copies the process environment into the map they return, so
// marvel adds no OTEL_EXPORTER_* key of its own even when the daemon has one.
func TestAnInheritedExporterVarIsNotAddedByMarvel(t *testing.T) {
	t.Setenv(resourceAttrs, "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://inherited.invalid:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "x-inherited=1")
	for name, env := range launchEnvs(t) {
		if keys := exporterKeys(env); len(keys) > 0 {
			t.Errorf("%s: the launch env carries %v while the daemon's env holds exporter vars", name, keys)
		}
	}
}

// The matcher itself: a planted key is found, and the report is of the key.
func TestExporterKeysFindsAPlantedKeyAndOnlyTheKey(t *testing.T) {
	env := map[string]string{
		"OTEL_RESOURCE_ATTRIBUTES":    "marvel.team=squad",
		"OTEL_SDK_DISABLED":           "true",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://secret.invalid",
		"OTEL_EXPORTER_OTLP_HEADERS":  "authorization=secret",
		"MY_OTEL_EXPORTER_X":          "not at the start",
	}
	got := exporterKeys(env)
	want := []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_HEADERS"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("exporterKeys = %v, want %v", got, want)
	}
}
