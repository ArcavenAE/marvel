package runtime

import (
	"errors"
	"os/exec"
	"regexp"
	"sort"
	"strings"
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
// where the harness has both. It also returns the names of the adapters it
// built, so a test can check the matrix against the registry.
func launchEnvs(t *testing.T) (envs map[string]map[string]string, built map[string]bool) {
	t.Helper()
	envs = map[string]map[string]string{}
	built = map[string]bool{}
	prep := func(name string, a Adapter, ctx *LaunchContext) {
		t.Helper()
		built[a.Name()] = true
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
	prep("forestage", &Forestage{}, testContext())
	oc := testContext()
	oc.Session.Runtime.Name, oc.Session.Runtime.Command = "opencode", "opencode"
	prep("opencode interactive", &OpenCode{}, oc)
	prep("opencode headless", &OpenCode{}, headlessContext("opencode", "opencode", ""))
	prep("simulator", &Simulator{}, simulatorContext())
	return envs, built
}

// Every adapter the registry holds is in the matrix, so a harness added later
// cannot be launched without this check reading its env. The registry is read
// here, not listed, so a new Register line fails this test until a row is added.
func TestLaunchMatrixCoversEveryRegisteredAdapter(t *testing.T) {
	t.Setenv(resourceAttrs, "")
	_, built := launchEnvs(t)
	reg := NewRegistry()
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	for name := range reg.adapters {
		if !built[name] {
			t.Errorf("the registry holds adapter %q and the launch matrix builds no env for it", name)
		}
	}
}

// A role that declares no telemetry env gets a seat with no OTEL_EXPORTER_*
// variable from marvel.
func TestUnsetKeyLaunchedSeatHasNoOTELExporterVar(t *testing.T) {
	t.Setenv(resourceAttrs, "")
	envs, _ := launchEnvs(t)
	for name, env := range envs {
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
	envs, _ := launchEnvs(t)
	for name, env := range envs {
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

// otlpExporterPrefix is where the OTLP exporters live. A package under it in
// marvel's dependency list means marvel's binary contains code that can open an
// OTLP connection.
const otlpExporterPrefix = "go.opentelemetry.io/otel/exporters/otlp"

// otelPrefix is the OpenTelemetry API: the positive control's evidence that a
// listing is a real one.
const otelPrefix = "go.opentelemetry.io/otel"

// depsOf lists the import paths a package depends on, from the package's own
// directory. A listing that fails, or is empty, fails the test, so the check
// below never reads nothing as a pass.
func depsOf(t *testing.T, pkg string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", pkg).Output()
	if err != nil {
		var stderr string
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("go list -deps %s: %v\n%s", pkg, err, stderr)
	}
	deps := strings.Fields(string(out))
	if len(deps) == 0 {
		t.Fatalf("go list -deps %s listed nothing, so the check would read nothing", pkg)
	}
	return deps
}

// withPrefix returns the entries of deps at or under prefix.
func withPrefix(deps []string, prefix string) []string {
	var hit []string
	for _, d := range deps {
		if d == prefix || strings.HasPrefix(d, prefix+"/") {
			hit = append(hit, d)
		}
	}
	return hit
}

// marvel's binary links no OTLP exporter, so it cannot open a telemetry
// connection of its own. The plan's broader claim, that a reconcile opens no
// connection beyond the daemon socket and the bus, is not proven here (the dial
// sites are spread over several packages and subprocesses, and Go has no
// process-wide dial hook). When marvel gains an exporter behind
// telemetry.otlp_endpoint (I-100 P3), this test changes on purpose.
func TestMarvelBinaryLinksNoOTLPExporter(t *testing.T) {
	// The positive control: the simulator links the OpenTelemetry API, so an
	// empty or failed listing cannot read as a pass.
	if len(withPrefix(depsOf(t, "../../cmd/simulator"), otelPrefix)) == 0 {
		t.Fatalf("the control listing of ../../cmd/simulator holds no %s package", otelPrefix)
	}
	if hit := withPrefix(depsOf(t, "../../cmd/marvel"), otlpExporterPrefix); len(hit) > 0 {
		t.Errorf("marvel links an OTLP exporter: %v", hit)
	}
}

// The matcher takes a path at or under the prefix and no neighbour of it.
func TestWithPrefixTakesTheTreeAndNoNeighbour(t *testing.T) {
	deps := []string{
		"go.opentelemetry.io/otel",
		"go.opentelemetry.io/otel/exporters/otlp",
		"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp",
		"go.opentelemetry.io/otel/exporters/otlpother",
		"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric",
	}
	got := withPrefix(deps, otlpExporterPrefix)
	want := []string{
		"go.opentelemetry.io/otel/exporters/otlp",
		"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp",
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("withPrefix = %v, want %v", got, want)
	}
}
