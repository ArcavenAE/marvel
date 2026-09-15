package runtime

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// The measured contract these tests encode (Claude Code 2.1.271, 2026-09-15):
// --session-id <fresh uuid> is accepted and names the transcript;
// --session-id <reused uuid> exits 1 "already in use"; --session-id
// not-a-uuid exits 1 "Invalid session ID". So the adapter must pass through
// exactly what marvel minted, exactly once, and must not mint over a caller
// who is steering session identity themselves.

func TestClaudeAssignsSessionIDByDefault(t *testing.T) {
	t.Parallel()
	c := &Claude{}
	ctx := testContext()
	ctx.Session.Runtime.Name = "claude"
	ctx.Session.Runtime.Command = "claude"

	if !c.AssignsSessionID(ctx) {
		t.Fatal("a plain claude launch should accept a marvel-assigned session id")
	}
}

func TestClaudeDeclinesSessionIDWhenCallerSteersIdentity(t *testing.T) {
	t.Parallel()
	// Each of these asks the harness to adopt or name a session itself, so
	// marvel must mint nothing rather than fight the caller's own args.
	cases := map[string][]string{
		"explicit session id":  {"--session-id", "11111111-2222-4333-8444-555555555555"},
		"joined session id":    {"--session-id=11111111-2222-4333-8444-555555555555"},
		"resume long":          {"--resume", "11111111-2222-4333-8444-555555555555"},
		"resume short":         {"-r"},
		"resume joined":        {"--resume=11111111-2222-4333-8444-555555555555"},
		"continue long":        {"--continue"},
		"continue short":       {"-c"},
		"identity flag buried": {"--verbose", "--continue", "--model", "opus"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := &Claude{}
			ctx := testContext()
			ctx.Session.Runtime.Name = "claude"
			ctx.Session.Runtime.Command = "claude"
			ctx.Session.Runtime.Args = args

			if c.AssignsSessionID(ctx) {
				t.Errorf("claude should decline an assigned id for args %v", args)
			}
		})
	}
}

func TestClaudePrepareInjectsAssignedSessionID(t *testing.T) {
	t.Parallel()
	c := &Claude{}
	ctx := testContext()
	ctx.Session.Runtime.Name = "claude"
	ctx.Session.Runtime.Command = "claude"
	ctx.HarnessSessionID = "11111111-2222-4333-8444-555555555555"

	result, err := c.Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !strings.Contains(result.Command, "--session-id 11111111-2222-4333-8444-555555555555") {
		t.Errorf("command should carry the assigned session id, got: %s", result.Command)
	}
	if n := strings.Count(result.Command, "--session-id"); n != 1 {
		t.Errorf("expected exactly 1 --session-id, got %d in: %s", n, result.Command)
	}
}

func TestClaudePrepareWithoutAssignedIDIsUnchanged(t *testing.T) {
	t.Parallel()
	c := &Claude{}
	ctx := testContext()
	ctx.Session.Runtime.Name = "claude"
	ctx.Session.Runtime.Command = "claude"

	result, err := c.Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	// An adapter handed no id must still produce a working command, the
	// same contract StreamPath has.
	if strings.Contains(result.Command, "--session-id") {
		t.Errorf("no id assigned, so no flag should appear, got: %s", result.Command)
	}
}

func TestClaudePrepareHeadlessAlsoCarriesAssignedID(t *testing.T) {
	t.Parallel()
	c := &Claude{}
	ctx := testContext()
	ctx.Session.Runtime.Name = "claude"
	ctx.Session.Runtime.Command = "claude"
	ctx.Session.Runtime.Mode = api.RuntimeModeHeadless
	ctx.Session.Runtime.Prompt = "do the thing"
	ctx.HarnessSessionID = "11111111-2222-4333-8444-555555555555"

	result, err := c.Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !strings.Contains(result.Command, "--session-id 11111111-2222-4333-8444-555555555555") {
		t.Errorf("headless launches write a transcript too and should be named, got: %s", result.Command)
	}
	// The prompt is the positional argument and must stay last.
	if !strings.HasSuffix(result.Command, "'do the thing'") {
		t.Errorf("prompt must remain the final positional arg, got: %s", result.Command)
	}
}

func TestAdaptersWithoutAnIDPinDoNotClaimOne(t *testing.T) {
	t.Parallel()
	// codex mints its own id and only exposes it to children, opencode and
	// generic have no pin at all. Implementing the interface for them would
	// record on the session an identity the harness was never told.
	for _, a := range []Adapter{&Codex{}, &OpenCode{}, &Generic{}} {
		if _, ok := a.(SessionIDAssigner); ok {
			t.Errorf("adapter %s claims to accept an assigned session id", a.Name())
		}
	}
}

func TestNewHarnessSessionIDShapeAndUniqueness(t *testing.T) {
	t.Parallel()
	// The harness rejects a malformed value outright, so the shape is part
	// of the contract, not cosmetics.
	seen := make(map[string]bool, 64)
	for i := 0; i < 64; i++ {
		id, err := api.NewHarnessSessionID()
		if err != nil {
			t.Fatalf("NewHarnessSessionID: %v", err)
		}
		parts := strings.Split(id, "-")
		if len(parts) != 5 ||
			len(parts[0]) != 8 || len(parts[1]) != 4 || len(parts[2]) != 4 ||
			len(parts[3]) != 4 || len(parts[4]) != 12 {
			t.Fatalf("not a UUID layout: %q", id)
		}
		if parts[2][0] != '4' {
			t.Errorf("expected version 4, got %q in %q", parts[2][0], id)
		}
		if !strings.ContainsRune("89ab", rune(parts[3][0])) {
			t.Errorf("expected RFC 4122 variant, got %q in %q", parts[3][0], id)
		}
		if strings.ToLower(id) != id {
			t.Errorf("id should be lowercase hex, got %q", id)
		}
		if seen[id] {
			t.Fatalf("minted a duplicate id %q; reuse is refused by the harness", id)
		}
		seen[id] = true
	}
}

func TestCodexDeclaresARelocatableHomeAndOthersDoNot(t *testing.T) {
	t.Parallel()
	// codex is the roster's case for containment rather than an id pin:
	// one variable relocates its whole state tree.
	c := &Codex{}
	assigner, ok := Adapter(c).(SessionHomeAssigner)
	if !ok {
		t.Fatal("codex should declare a relocatable session home")
	}
	spec, want := assigner.SessionHome(testContext())
	if !want {
		t.Fatal("codex should want a private home")
	}
	if spec.EnvVar != "CODEX_HOME" {
		t.Errorf("expected CODEX_HOME as the lever, got %q", spec.EnvVar)
	}
	// Measured: a private home with no auth.json is a 401 on the first
	// model call, so the credential link is not optional decoration.
	found := false
	for _, n := range spec.LinkIn {
		if n == "auth.json" {
			found = true
		}
	}
	if !found {
		t.Errorf("codex must link auth.json into a private home, got LinkIn %v", spec.LinkIn)
	}

	// claude scatters state across ~/.claude and per-project directories
	// with no single relocatable root, so it must not claim one.
	for _, a := range []Adapter{&Claude{}, &OpenCode{}, &Generic{}} {
		if _, claims := a.(SessionHomeAssigner); claims {
			t.Errorf("adapter %s claims a relocatable session home", a.Name())
		}
	}
}

func TestCodexPreparePutsThePrivateHomeInTheEnvironment(t *testing.T) {
	t.Parallel()
	c := &Codex{}
	ctx := testContext()
	ctx.Session.Runtime.Name = "codex"
	ctx.Session.Runtime.Command = "codex"
	ctx.HarnessHomePath = "/tmp/marvel-harness-homes/acme-squad-coder-g1-0"

	result, err := c.Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if result.Env["CODEX_HOME"] != ctx.HarnessHomePath {
		t.Errorf("CODEX_HOME should be the assigned home, got %q", result.Env["CODEX_HOME"])
	}
	// Identity still rides the environment, which the hook subprocess
	// inherits (measured on 0.153.4).
	if result.Env["MARVEL_SESSION"] == "" {
		t.Error("the private home must not displace the identity stamp")
	}
}

func TestCodexPrepareWithoutAPrivateHomeIsUnchanged(t *testing.T) {
	t.Parallel()
	c := &Codex{}
	ctx := testContext()
	ctx.Session.Runtime.Name = "codex"
	ctx.Session.Runtime.Command = "codex"

	result, err := c.Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if _, set := result.Env["CODEX_HOME"]; set {
		t.Errorf("no home assigned, so CODEX_HOME should be unset, got %q", result.Env["CODEX_HOME"])
	}
}
