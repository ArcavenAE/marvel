package runtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// aae-orc-mdr0n, spec S1 to S13 (operator ruling I-35): marvel stamps the
// seat's identity into OTEL_RESOURCE_ATTRIBUTES at launch for the harnesses
// whose telemetry reads it, so a harness's own telemetry says which seat it
// came from.

const resourceAttrs = "OTEL_RESOURCE_ATTRIBUTES"

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// pairs splits an OTEL_RESOURCE_ATTRIBUTES value into its raw key=value
// pairs, in order. The values stay percent-encoded.
func pairs(v string) []string {
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}

func stampOf(t *testing.T, env map[string]string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, p := range pairs(env[resourceAttrs]) {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			t.Fatalf("pair %q has no =", p)
		}
		if strings.HasPrefix(k, "marvel.") {
			out[k] = v
		}
	}
	return out
}

func prepareStamped(t *testing.T, ctx *LaunchContext) *LaunchResult {
	t.Helper()
	r, err := (&Claude{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return r
}

// S4, S1: the four fields marvel always knows are stamped, and a seat URI or a
// cluster that is not there is left out, never sent empty.
func TestClaudeStampsTheSeatWithoutASeatURIOrCluster(t *testing.T) {
	t.Setenv(resourceAttrs, "")
	env := prepareStamped(t, claudeCtx("claude")).Env
	want := "marvel.workspace=acme,marvel.team=squad,marvel.role=worker,marvel.session=squad-worker-g1-0"
	// The default system prompt is marvel's own, so its hash rides too.
	got := env[resourceAttrs]
	if !strings.HasPrefix(got, want) {
		t.Fatalf("%s = %q, want it to start with %q", resourceAttrs, got, want)
	}
	for _, key := range []string{"marvel.seat", "marvel.cluster", "marvel.first_prompt.sha256", "marvel.prompt_artifact.sha256"} {
		if _, there := stampOf(t, env)[key]; there {
			t.Errorf("%s is stamped with no value to carry", key)
		}
	}
	if strings.Contains(got, "=,") || strings.HasSuffix(got, "=") {
		t.Errorf("an empty value is stamped: %q", got)
	}
}

func TestClaudeStampsTheSeatURIAndCluster(t *testing.T) {
	t.Setenv(resourceAttrs, "")
	ctx := claudeCtx("claude")
	ctx.Cluster = "c1"
	env := prepareStamped(t, ctx).Env
	got := stampOf(t, env)
	if got["marvel.seat"] != "marvel://c1/acme/squad/worker-0" {
		t.Errorf("marvel.seat = %q", got["marvel.seat"])
	}
	if got["marvel.seat"] != env[seatURIEnv] {
		t.Errorf("marvel.seat %q is not %s %q", got["marvel.seat"], seatURIEnv, env[seatURIEnv])
	}
	if got["marvel.cluster"] != "c1" {
		t.Errorf("marvel.cluster = %q", got["marvel.cluster"])
	}
}

// S4: the role's declared value comes first, kept as written, and marvel's
// pairs follow it.
func TestClaudeKeepsARoleDeclaredResourceAttributesAndAppends(t *testing.T) {
	t.Setenv(resourceAttrs, "from.daemon=1")
	ctx := claudeCtx("claude")
	ctx.Role.Runtime.Env = map[string]string{resourceAttrs: "service.namespace=ops,deployment.environment=prod"}
	got := pairs(prepareStamped(t, ctx).Env[resourceAttrs])
	if len(got) < 3 || got[0] != "service.namespace=ops" || got[1] != "deployment.environment=prod" || !strings.HasPrefix(got[2], "marvel.workspace=") {
		t.Errorf("pairs = %v, want the declared pairs first, then marvel's", got)
	}
	for _, p := range got {
		if p == "from.daemon=1" {
			t.Errorf("the daemon's value was merged although the role declared its own")
		}
	}
}

// S4: with no declared value the daemon's own is the existing one.
func TestClaudeKeepsTheDaemonsResourceAttributesWhenTheRoleDeclaresNone(t *testing.T) {
	t.Setenv(resourceAttrs, "from.daemon=1,k=v")
	got := pairs(prepareStamped(t, claudeCtx("claude")).Env[resourceAttrs])
	if len(got) < 3 || got[0] != "from.daemon=1" || got[1] != "k=v" || !strings.HasPrefix(got[2], "marvel.workspace=") {
		t.Errorf("pairs = %v, want the daemon's pairs first, then marvel's", got)
	}
}

// S5: a marvel.* key the role declared is dropped, marvel's own value wins,
// and the log names the key and never the value.
func TestClaudeDropsARoleDeclaredMarvelKeyAndLogsTheKeyOnly(t *testing.T) {
	t.Setenv(resourceAttrs, "")
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(log.Writer()) })

	ctx := claudeCtx("claude")
	ctx.Role.Runtime.Env = map[string]string{resourceAttrs: "marvel.role=hidden-value,keep.me=1"}
	env := prepareStamped(t, ctx).Env
	if got := stampOf(t, env)["marvel.role"]; got != "worker" {
		t.Errorf("marvel.role = %q, want marvel's own value", got)
	}
	if !strings.Contains(env[resourceAttrs], "keep.me=1") {
		t.Errorf("a non-marvel pair was dropped: %q", env[resourceAttrs])
	}
	if strings.Contains(env[resourceAttrs], "hidden-value") {
		t.Errorf("the declared marvel.role value survived: %q", env[resourceAttrs])
	}
	if !strings.Contains(buf.String(), "marvel.role") {
		t.Errorf("the drop was not logged by key: %q", buf.String())
	}
	if strings.Contains(buf.String(), "hidden-value") {
		t.Errorf("the log carries the dropped value: %q", buf.String())
	}
}

// S2: a value is percent-encoded for the comma, the equals sign, the percent
// sign and whitespace, and a plain value is left readable.
func TestStampValuesArePercentEncoded(t *testing.T) {
	t.Setenv(resourceAttrs, "")
	ctx := claudeCtx("claude")
	ctx.Session.Name = "a b,c=d%e"
	got := stampOf(t, prepareStamped(t, ctx).Env)
	if want := "a%20b%2Cc%3Dd%25e"; got["marvel.session"] != want {
		t.Errorf("marvel.session = %q, want %q", got["marvel.session"], want)
	}
}

// S3, S12, S13: the prompt hashes are of the exact bytes passed, hex
// lowercase, and the session record carries the same values as the stamp.
func TestClaudeStampsTheSystemPromptMarvelAppended(t *testing.T) {
	t.Setenv(resourceAttrs, "")
	r := prepareStamped(t, claudeCtx("claude"))
	const text = "You are squad-worker-g1-0 (role: worker, team: squad, workspace: acme)."
	if got := stampOf(t, r.Env)["marvel.system_prompt.sha256"]; got != sha(text) {
		t.Errorf("env system prompt hash = %q, want %q", got, sha(text))
	}
	if r.SystemPromptSha256 != sha(text) {
		t.Errorf("record system prompt hash = %q, want %q", r.SystemPromptSha256, sha(text))
	}
}

func TestClaudeStampsASystemPromptTheRoleCarriesInItsArgs(t *testing.T) {
	t.Setenv(resourceAttrs, "")
	for name, args := range map[string][]string{
		"separate":      {"--append-system-prompt", "role words"},
		"joined":        {"--append-system-prompt=role words"},
		"after another": {"--model", "sonnet", "--append-system-prompt", "role words"},
	} {
		ctx := claudeCtx("claude")
		ctx.Session.Runtime.Args = args
		r := prepareStamped(t, ctx)
		if got := stampOf(t, r.Env)["marvel.system_prompt.sha256"]; got != sha("role words") {
			t.Errorf("%s: hash = %q, want the role's text", name, got)
		}
		if r.SystemPromptSha256 != sha("role words") {
			t.Errorf("%s: record hash = %q", name, r.SystemPromptSha256)
		}
	}
}

func TestClaudeStampsASystemPromptInTheCommandWords(t *testing.T) {
	t.Setenv(resourceAttrs, "")
	ctx := claudeCtx("claude --append-system-prompt inline")
	if got := stampOf(t, prepareStamped(t, ctx).Env)["marvel.system_prompt.sha256"]; got != sha("inline") {
		t.Errorf("hash = %q, want the inline word", got)
	}
}

// S3: marvel cannot read a wrapper command or a prompt file as text it passed,
// so it stamps no hash for either.
func TestClaudeStampsNoSystemPromptHashItCannotRead(t *testing.T) {
	t.Setenv(resourceAttrs, "")
	cases := map[string]*LaunchContext{
		"a wrapper command": claudeCtx("/opt/wrap/run.sh 'quoted words' | tee"),
		"a prompt file": func() *LaunchContext {
			c := claudeCtx("claude")
			c.Session.Runtime.Args = []string{"--append-system-prompt-file", "/etc/role.md"}
			return c
		}(),
	}
	for name, ctx := range cases {
		r := prepareStamped(t, ctx)
		if _, there := stampOf(t, r.Env)["marvel.system_prompt.sha256"]; there {
			t.Errorf("%s: a system prompt hash was stamped", name)
		}
		if r.SystemPromptSha256 != "" {
			t.Errorf("%s: the record holds a hash: %q", name, r.SystemPromptSha256)
		}
	}
}

func TestClaudeStampsTheFirstPromptHash(t *testing.T) {
	t.Setenv(resourceAttrs, "")
	const p = "summarize the repo"
	for name, mode := range map[string]api.RuntimeMode{"headless": api.RuntimeModeHeadless, "interactive": api.RuntimeModeInteractive} {
		r := prepareStamped(t, claudePromptCtx("claude", mode, p, nil))
		if got := stampOf(t, r.Env)["marvel.first_prompt.sha256"]; got != sha(p) {
			t.Errorf("%s: env hash = %q, want %q", name, got, sha(p))
		}
		if r.FirstPrompt.Sha256 != sha(p) {
			t.Errorf("%s: record hash = %q", name, r.FirstPrompt.Sha256)
		}
	}
}

// A first prompt handed to a wrapper in the environment is still the message
// marvel composed, so the same hash is stamped, and the text stays in
// MARVEL_FIRST_PROMPT only.
func TestClaudeStampsTheHashOfAFirstPromptDeliveredThroughTheEnvironment(t *testing.T) {
	t.Setenv(resourceAttrs, "")
	r := prepareStamped(t, claudePromptCtx("/opt/wrap/run.sh", api.RuntimeModeInteractive, "go on", nil))
	if got := stampOf(t, r.Env)["marvel.first_prompt.sha256"]; got != sha(r.Env[firstPromptEnv]) || got == "" {
		t.Errorf("hash = %q, want the hash of %s", got, firstPromptEnv)
	}
	if strings.Contains(r.Env[resourceAttrs], "go%20on") || strings.Contains(r.Env[resourceAttrs], "go on") {
		t.Errorf("prompt text reached %s: %q", resourceAttrs, r.Env[resourceAttrs])
	}
}

func TestClaudeStampsTheHashOfTheComposedSuccessorMessage(t *testing.T) {
	t.Setenv(resourceAttrs, "")
	ctx := claudePromptCtx("claude", api.RuntimeModeInteractive, "carry on", nil)
	ctx.Session.Predecessor = "squad-worker-g1-0"
	text, _, _ := composeFirstPrompt(ctx)
	r := prepareStamped(t, ctx)
	if got := stampOf(t, r.Env)["marvel.first_prompt.sha256"]; got != sha(text) {
		t.Errorf("hash = %q, want the hash of the composed message", got)
	}
	if r.FirstPrompt.Sha256 != sha(text) {
		t.Errorf("record hash = %q", r.FirstPrompt.Sha256)
	}
}

// S3: no prompt, no hash, in the stamp or in the record.
func TestClaudeStampsNoFirstPromptHashWhenNoneWasPassed(t *testing.T) {
	t.Setenv(resourceAttrs, "")
	r := prepareStamped(t, claudePromptCtx("claude", api.RuntimeModeInteractive, "", nil))
	if _, there := stampOf(t, r.Env)["marvel.first_prompt.sha256"]; there {
		t.Errorf("a first prompt hash was stamped with no prompt")
	}
	if r.FirstPrompt.Sha256 != "" {
		t.Errorf("record hash = %q, want none", r.FirstPrompt.Sha256)
	}
}

// S3: no prompt text is ever written to the stamp.
func TestStampCarriesNoPromptText(t *testing.T) {
	t.Setenv(resourceAttrs, "")
	r := prepareStamped(t, claudePromptCtx("claude", api.RuntimeModeHeadless, "very private request", nil))
	for _, word := range []string{"very", "private", "request", "You%20are", "You are"} {
		if strings.Contains(r.Env[resourceAttrs], word) {
			t.Errorf("%q reached %s: %q", word, resourceAttrs, r.Env[resourceAttrs])
		}
	}
}

// S6, S11: marvel names no exporter and does not switch telemetry on.
func TestStampTurnsNoTelemetryOn(t *testing.T) {
	t.Setenv(resourceAttrs, "")
	for name, env := range map[string]map[string]string{
		"claude": prepareStamped(t, claudeCtx("claude")).Env,
		"codex":  prepareStampedCodex(t, codexCtx(api.RuntimeModeInteractive, "")).Env,
	} {
		for k := range env {
			if k == "CLAUDE_CODE_ENABLE_TELEMETRY" || (strings.HasPrefix(k, "OTEL_") && k != resourceAttrs) {
				t.Errorf("%s: marvel set %s", name, k)
			}
		}
	}
}

func codexCtx(mode api.RuntimeMode, prompt string) *LaunchContext {
	ctx := testContext()
	ctx.Session.Runtime.Name = "codex"
	ctx.Session.Runtime.Command = "codex"
	ctx.Session.Runtime.Args = nil
	ctx.Session.Runtime.Mode = mode
	ctx.Session.Runtime.Prompt = prompt
	return ctx
}

func prepareStampedCodex(t *testing.T, ctx *LaunchContext) *LaunchResult {
	t.Helper()
	r, err := (&Codex{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return r
}

// S8: codex 0.160.1 reads the variable (measured, PR body), so it gets the same
// stamp, with the first prompt hash where a prompt was passed and no system
// prompt hash, which codex has none of.
func TestCodexGetsTheSameStamp(t *testing.T) {
	t.Setenv(resourceAttrs, "")
	r := prepareStampedCodex(t, codexCtx(api.RuntimeModeHeadless, "run the job"))
	got := stampOf(t, r.Env)
	if got["marvel.role"] != "worker" || got["marvel.session"] != "squad-worker-g1-0" {
		t.Errorf("codex stamp = %v", got)
	}
	if got["marvel.first_prompt.sha256"] != sha("run the job") || r.FirstPrompt.Sha256 != sha("run the job") {
		t.Errorf("first prompt hash: env %q, record %q", got["marvel.first_prompt.sha256"], r.FirstPrompt.Sha256)
	}
	if _, there := got["marvel.system_prompt.sha256"]; there {
		t.Errorf("codex was stamped with a system prompt hash")
	}
}

func TestCodexInteractiveIsStampedWithoutAFirstPromptHash(t *testing.T) {
	t.Setenv(resourceAttrs, "")
	got := stampOf(t, prepareStampedCodex(t, codexCtx(api.RuntimeModeInteractive, "")).Env)
	if got["marvel.session"] != "squad-worker-g1-0" {
		t.Errorf("codex stamp = %v", got)
	}
	if _, there := got["marvel.first_prompt.sha256"]; there {
		t.Errorf("a first prompt hash was stamped with none passed")
	}
}

// The spec stamps claude and codex only; the other adapters' telemetry is not
// claimed to read the variable.
func TestOtherAdaptersAreNotStamped(t *testing.T) {
	t.Setenv(resourceAttrs, "")
	for _, a := range []Adapter{&Forestage{}, &Generic{}, &OpenCode{}} {
		ctx := testContext()
		ctx.Session.Runtime.Name = a.Name()
		ctx.Session.Runtime.Command = "/usr/local/bin/" + a.Name()
		r, err := a.Prepare(ctx)
		if err != nil {
			continue
		}
		if v, there := r.Env[resourceAttrs]; there {
			t.Errorf("%s was stamped: %q", a.Name(), v)
		}
	}
}

// The constructed environment names the variables codex forwards to the
// director server. The stamp is built after it, so it is not in that list.
func TestStampIsNotAConstructedVariable(t *testing.T) {
	t.Setenv(resourceAttrs, "")
	if _, there := baseEnv(testContext())[resourceAttrs]; there {
		t.Errorf("baseEnv carries %s, so every adapter would be stamped", resourceAttrs)
	}
}

// Spec section 6: the role library's prompt-artifact hash rides as
// marvel.prompt_artifact.sha256 when the manifest declares it, for claude and
// codex, and is left out otherwise.
func TestStampCarriesThePromptArtifactHashWhenDeclared(t *testing.T) {
	t.Setenv(resourceAttrs, "")
	want := strings.Repeat("c", 64)
	for name, env := range map[string]map[string]string{
		"claude": func() map[string]string {
			ctx := claudeCtx("claude")
			ctx.Session.Runtime.PromptArtifactSha256 = want
			return prepareStamped(t, ctx).Env
		}(),
		"codex": func() map[string]string {
			ctx := codexCtx(api.RuntimeModeInteractive, "")
			ctx.Session.Runtime.PromptArtifactSha256 = want
			return prepareStampedCodex(t, ctx).Env
		}(),
	} {
		if got := stampOf(t, env)["marvel.prompt_artifact.sha256"]; got != want {
			t.Errorf("%s: marvel.prompt_artifact.sha256 = %q, want %q", name, got, want)
		}
	}
	if _, there := stampOf(t, prepareStamped(t, claudeCtx("claude")).Env)["marvel.prompt_artifact.sha256"]; there {
		t.Errorf("a hash was stamped with none declared")
	}
}

// CodeQL on the stamp: the merged env map also holds the bus password, so the
// existing value is read from the role's declaration or the daemon's own
// environment and never from that map. A poisoned entry in the map must not be
// seen.
func TestStampReadsTheRoleDeclaredValueNotTheMergedMap(t *testing.T) {
	t.Setenv(resourceAttrs, "from.daemon=1")
	ctx := claudeCtx("claude")
	ctx.Role.Runtime.Env = map[string]string{resourceAttrs: "from.role=1"}
	env := map[string]string{resourceAttrs: "from.map=1", "DIRECTOR_NATS_PASS": "x"}
	stampSeat(ctx, env, "", "")
	got := env[resourceAttrs]
	if !strings.HasPrefix(got, "from.role=1,marvel.workspace=") {
		t.Errorf("%s = %q, want the role's declared pairs first", resourceAttrs, got)
	}
	if strings.Contains(got, "from.map") || strings.Contains(got, "from.daemon") {
		t.Errorf("%s = %q, want neither the merged map's value nor the daemon's", resourceAttrs, got)
	}
}

func TestStampReadsTheDaemonsValueNotTheMergedMapWhenTheRoleDeclaresNone(t *testing.T) {
	t.Setenv(resourceAttrs, "from.daemon=1")
	ctx := claudeCtx("claude")
	env := map[string]string{resourceAttrs: "from.map=1"}
	stampSeat(ctx, env, "", "")
	got := env[resourceAttrs]
	if !strings.HasPrefix(got, "from.daemon=1,marvel.workspace=") || strings.Contains(got, "from.map") {
		t.Errorf("%s = %q, want the daemon's pairs first and not the map's", resourceAttrs, got)
	}
}

// The stamp's log line names a dropped key and nothing else, so a bus password
// the launch environment holds never reaches it, or the stamp.
func TestStampLogLineNeverCarriesTheBusPassword(t *testing.T) {
	t.Setenv(resourceAttrs, "")
	const password = "hunter2-bus-password-9f3a"
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(log.Writer()) })

	ctx := claudeCtx("claude")
	ctx.BusURL, ctx.BusUser, ctx.BusPassword = "nats://127.0.0.1:4222", "squad", password
	ctx.Role.Runtime.Env = map[string]string{resourceAttrs: "marvel.role=x,marvel.team=y,keep=1"}
	r := prepareStamped(t, ctx)

	if r.Env["DIRECTOR_NATS_PASS"] != password {
		t.Fatalf("setup: the launch environment does not hold the password")
	}
	if !strings.Contains(buf.String(), "marvel.role") || !strings.Contains(buf.String(), "marvel.team") {
		t.Errorf("the dropped keys were not logged: %q", buf.String())
	}
	if strings.Contains(buf.String(), password) {
		t.Errorf("the log carries the bus password: %q", buf.String())
	}
	if strings.Contains(r.Env[resourceAttrs], password) {
		t.Errorf("the stamp carries the bus password")
	}
}
