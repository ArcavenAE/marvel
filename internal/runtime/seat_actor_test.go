package runtime

import (
	"fmt"
	"strings"
	"testing"
)

// captureSeatLog swaps the seam the adapter logs a mint decision through.
func captureSeatLog(t *testing.T) *[]string {
	t.Helper()
	var lines []string
	old := logSeat
	logSeat = func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	t.Cleanup(func() { logSeat = old })
	return &lines
}

func seatContext(session string) *LaunchContext {
	ctx := testContext()
	ctx.Session.Name = session
	ctx.Cluster = "c1"
	return ctx
}

// TestSpawnEnvCarriesBeadsActorAsSlotURI: aae-orc-2pss4. The actor is the
// seat's slot URI, and it is the same string for generation N and N+1 of one
// slot, so a successor reads its predecessor's claims as its own.
func TestSpawnEnvCarriesBeadsActorAsSlotURI(t *testing.T) {
	const want = "marvel://c1/acme/squad/worker-0"
	for _, name := range []string{"squad-worker-g1-0", "squad-worker-g2-0", "squad-worker-g17-0"} {
		env := baseEnv(seatContext(name))
		if got := env["BEADS_ACTOR"]; got != want {
			t.Errorf("%s: BEADS_ACTOR = %q, want %q", name, got, want)
		}
		if got := env[seatURIEnv]; got != want {
			t.Errorf("%s: %s = %q, want the same URI as BEADS_ACTOR", name, seatURIEnv, got)
		}
	}
	if got := baseEnv(seatContext("squad-worker-g1-2"))["BEADS_ACTOR"]; got != "marvel://c1/acme/squad/worker-2" {
		t.Errorf("replica 2: BEADS_ACTOR = %q", got)
	}
}

// With no cluster name marvel mints no URI: the actor keeps today's value and
// one line says why. Nothing is defaulted. A cluster with a valid name always
// mints; no hub section or fleet key is needed.
func TestSpawnEnvKeepsTodaysActorWithoutACluster(t *testing.T) {
	const today = "marvel/acme/squad-worker-g1-0"
	lines := captureSeatLog(t)
	ctx := seatContext("squad-worker-g1-0")
	ctx.Cluster = ""
	env := baseEnv(ctx)
	if got := env["BEADS_ACTOR"]; got != today {
		t.Errorf("BEADS_ACTOR = %q, want today's %q", got, today)
	}
	if v, ok := env[seatURIEnv]; ok {
		t.Errorf("%s = %q, want it unset with no cluster", seatURIEnv, v)
	}
	if len(*lines) != 1 || !strings.Contains((*lines)[0], "cluster entry name") {
		t.Errorf("log lines = %q, want exactly one naming the cluster", *lines)
	}
}

// A string the builder refuses is never minted: the fallback applies and the
// line carries the builder's own error.
func TestSpawnEnvRefusesAnActorTheBuilderRejects(t *testing.T) {
	cases := map[string]func(*LaunchContext){
		"uppercase cluster":  func(c *LaunchContext) { c.Cluster = "C1" },
		"underscore cluster": func(c *LaunchContext) { c.Cluster = "c_1" },
		"held replica name":  func(c *LaunchContext) { c.Session.Name = "squad-worker-g1-held-0" },
		"role with gen tail": func(c *LaunchContext) {
			c.Role.Name = "widget-g4"
			c.Session.Role = "widget-g4"
			c.Session.Name = "squad-widget-g4-g1-0"
		},
		"name with no index":     func(c *LaunchContext) { c.Session.Name = "squad-worker" },
		"non-numeric generation": func(c *LaunchContext) { c.Session.Name = "squad-worker-gx-0" },
		"empty generation":       func(c *LaunchContext) { c.Session.Name = "squad-worker-g-0" },
		"empty index":            func(c *LaunchContext) { c.Session.Name = "squad-worker-g1-" },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			lines := captureSeatLog(t)
			ctx := seatContext("squad-worker-g1-0")
			edit(ctx)
			env := baseEnv(ctx)
			if got := env["BEADS_ACTOR"]; strings.HasPrefix(got, "marvel://") {
				t.Errorf("BEADS_ACTOR = %q, a URI was minted from a string the builder rejects", got)
			}
			if len(*lines) != 1 {
				t.Errorf("log lines = %q, want exactly one", *lines)
			}
			if v, ok := env[seatURIEnv]; ok {
				t.Errorf("%s = %q, want it unset when no URI is minted", seatURIEnv, v)
			}
		})
	}
}

// The role's declared override still wins over the minted value.
func TestSpawnEnvActorOverrideStillWins(t *testing.T) {
	ctx := seatContext("squad-worker-g1-0")
	ctx.Role.Runtime.Env = map[string]string{"BEADS_ACTOR": "operator/explicit"}
	if got := baseEnv(ctx)["BEADS_ACTOR"]; got != "operator/explicit" {
		t.Errorf("BEADS_ACTOR = %q, want the declared override", got)
	}
}

// A codex launch builds the constructed environment twice, once to seed the
// director hook's forwarded names and once for the launch itself. A refused
// mint is still one launch, so it logs one line.
func TestCodexLaunchLogsARefusedMintOnce(t *testing.T) {
	lines := captureSeatLog(t)
	ctx := seatContext("squad-worker-g1-0")
	ctx.Cluster = ""
	ctx.Session.Runtime.Command = "codex"
	_ = codexForwardedEnv(ctx)
	if _, err := (&Codex{}).Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if len(*lines) != 1 {
		t.Errorf("log lines = %q, want exactly one for the launch", *lines)
	}
}

// The names the seeder forwards match the launch environment's, with the same
// URI minted or refused.
func TestCodexForwardedNamesIncludeTheSeatURI(t *testing.T) {
	names := codexForwardedEnv(seatContext("squad-worker-g1-0"))
	found := false
	for _, n := range names {
		if n == seatURIEnv {
			found = true
		}
	}
	if !found {
		t.Errorf("forwarded names %v lack %s", names, seatURIEnv)
	}
}
