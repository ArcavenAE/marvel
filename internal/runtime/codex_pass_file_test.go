package runtime

import (
	"slices"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

func busCtx() *LaunchContext {
	return &LaunchContext{
		Session:   &api.Session{Name: "t-r-g1-0", Workspace: "ws", Team: "t"},
		Role:      &api.Role{Name: "r"},
		Team:      &api.Team{Name: "t"},
		Workspace: &api.Workspace{Name: "ws"},
		BusURL:    "nats://127.0.0.1:4222",
		BusUser:   "t",
	}
}

// A launch whose bus password goes by file forwards the file variable to the
// director server, and not the value variable the launch no longer sets.
func TestCodexForwardsThePasswordFileNotThePasswordWhenItGoesByFile(t *testing.T) {
	t.Parallel()
	ctx := busCtx()
	ctx.BusPassword = "pw"
	ctx.BusPasswordFile = "/run/marvel/tokens/abc.pass"

	names := codexForwardedEnv(ctx)

	if !slices.Contains(names, "DIRECTOR_NATS_PASS_FILE") {
		t.Errorf("forwarded names %v lack DIRECTOR_NATS_PASS_FILE", names)
	}
	if slices.Contains(names, "DIRECTOR_NATS_PASS") {
		t.Errorf("forwarded names %v name DIRECTOR_NATS_PASS, which the launch does not set", names)
	}
	if !slices.IsSorted(names) {
		t.Errorf("forwarded names %v are not sorted", names)
	}
}

// Every forwarded name is one the constructed environment carries, so the
// director server is never told to forward a variable that is absent.
func TestEveryForwardedNameIsInTheConstructedEnvironment(t *testing.T) {
	t.Parallel()
	for name, set := range map[string]func(*LaunchContext){
		"by file":                        func(c *LaunchContext) { c.BusPassword, c.BusPasswordFile = "pw", "/p/abc.pass" },
		"by value":                       func(c *LaunchContext) { c.BusPassword = "pw" },
		"no password (file not written)": func(c *LaunchContext) {},
	} {
		ctx := busCtx()
		set(ctx)
		env := constructedEnvQuiet(ctx, true)
		for _, n := range codexForwardedEnv(ctx) {
			if _, ok := env[n]; !ok {
				t.Errorf("%s: %s is forwarded but absent from the environment", name, n)
			}
		}
	}
}
