package runtime

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/paths"
	"github.com/arcavenae/marvel/internal/shorttmp"
)

// A role's runtime.env must not move where a seat presents the bus and daemon
// credentials marvel hands it (aae-orc-kx73y). NATS_URL, MARVEL_SOCKET and the
// marvel home are cluster topology, set from the cluster config and the
// daemon's own layout. A manifest able to replace them could point a seat at a
// listener of its choosing, so whoever can apply a manifest would collect the
// broker and heartbeat credentials without reading daemon state.

var topologyKeys = []string{"NATS_URL", "MARVEL_SOCKET", paths.HomeEnv}

func topologyContext(t *testing.T) (*LaunchContext, map[string]string) {
	t.Helper()
	home := shorttmp.Dir(t)
	t.Setenv(paths.HomeEnv, home)
	ctx := testContext()
	ctx.BusURL = "nats://127.0.0.1:4222"
	ctx.BusUser, ctx.BusPassword = "agent", "pw"
	ctx.SocketPath = "/run/marvel.sock"
	return ctx, map[string]string{
		"NATS_URL":      ctx.BusURL,
		"MARVEL_SOCKET": ctx.SocketPath,
		paths.HomeEnv:   home,
	}
}

// TestRoleEnvCannotMoveTheTopologyMarvelConstructed: each key keeps the value
// marvel constructed, and the refusal names the key in the log and never the
// value the role tried to set.
func TestRoleEnvCannotMoveTheTopologyMarvelConstructed(t *testing.T) {
	for _, key := range topologyKeys {
		t.Run(key, func(t *testing.T) {
			buf := captureLog(t)
			ctx, want := topologyContext(t)
			ctx.Role.Runtime.Env = map[string]string{key: "nats://attacker.example:4222"}

			env := baseEnv(ctx)
			if env[key] != want[key] {
				t.Errorf("%s = %q after a role env override, want the constructed %q", key, env[key], want[key])
			}
			if !strings.Contains(buf.String(), key) || !strings.Contains(buf.String(), "refused") {
				t.Errorf("%s: log %q does not name the key as refused", key, buf.String())
			}
			if strings.Contains(buf.String(), "attacker.example") {
				t.Errorf("%s: the log echoed the refused value", key)
			}
		})
	}
}

// TestRoleEnvTopologyIsRefusedEvenWhereMarvelBuiltNoValue: a cluster with no
// managed bus builds no NATS_URL, and one run without a daemon socket builds no
// MARVEL_SOCKET. The role still may not supply them, since the refusal is about
// who owns the key and not whether a value happens to be present.
func TestRoleEnvTopologyIsRefusedEvenWhereMarvelBuiltNoValue(t *testing.T) {
	for _, key := range topologyKeys {
		t.Run(key, func(t *testing.T) {
			captureLog(t)
			t.Setenv(paths.HomeEnv, "")
			ctx := testContext()
			ctx.BusURL, ctx.SocketPath = "", ""
			ctx.Role.Runtime.Env = map[string]string{key: "elsewhere"}

			if v, present := baseEnv(ctx)[key]; present {
				t.Errorf("%s = %q was carried from a role env where marvel built none", key, v)
			}
		})
	}
}

// TestRoleEnvStillOverridesWhatItMay: the refusal is the three topology keys
// and no more. A neighbouring name and an ordinary variable still pass.
func TestRoleEnvStillOverridesWhatItMay(t *testing.T) {
	captureLog(t)
	ctx, _ := topologyContext(t)
	ctx.Role.Runtime.Env = map[string]string{"DIRECTOR_NATS_URL": "x", "EDITOR": "vi"}

	env := baseEnv(ctx)
	if env["DIRECTOR_NATS_URL"] != "x" || env["EDITOR"] != "vi" {
		t.Errorf("an unrelated role env was refused: DIRECTOR_NATS_URL=%q EDITOR=%q", env["DIRECTOR_NATS_URL"], env["EDITOR"])
	}
}
