package runtime

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// marvel#391, docs/design/interactive-first-prompt.md T4 (D2, D3, D4): a
// session that replaces another is launched with a fixed first message written
// only from values marvel holds, ahead of the role's own runtime.prompt.

const predecessorKey = "acme/squad-worker-g1-0"

func successorCtx(command, rolePrompt string) *LaunchContext {
	ctx := claudePromptCtx(command, api.RuntimeModeInteractive, rolePrompt, nil)
	ctx.Session.Name = "squad-worker-g2-0"
	ctx.Session.Predecessor = predecessorKey
	return ctx
}

func lastWord(t *testing.T, command string) string {
	t.Helper()
	i := strings.LastIndex(command, " '")
	if i < 0 {
		t.Fatalf("no quoted last word in %s", command)
	}
	return command[i+1:]
}

func TestSuccessorFirstMessageNamesThePredecessor(t *testing.T) {
	t.Parallel()
	result, err := (&Claude{}).Prepare(successorCtx("claude", ""))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	last := lastWord(t, result.Command)
	if !strings.Contains(last, "You are a successor to "+predecessorKey+".") {
		t.Errorf("first message does not name the predecessor: %s", last)
	}
	if !strings.Contains(last, "Read its handoff first, if your process names one.") {
		t.Errorf("no generic handoff line: %s", last)
	}
}

func TestSuccessorFirstMessageNamesTheResolvedHandoffPath(t *testing.T) {
	t.Parallel()
	ctx := successorCtx("claude", "")
	ctx.Role.Shift = &api.ShiftPolicy{Handoff: "/var/handoff/{session}.md"}
	result, err := (&Claude{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	last := lastWord(t, result.Command)
	if !strings.Contains(last, "Read its handoff at /var/handoff/squad-worker-g1-0.md first.") {
		t.Errorf("handoff path is not the predecessor's resolved path: %s", last)
	}
	if strings.Contains(last, "squad-worker-g2-0.md") {
		t.Errorf("handoff path resolved against the successor, not the predecessor: %s", last)
	}
	if strings.Contains(last, "if your process names one") {
		t.Errorf("generic line present beside a declared path: %s", last)
	}
}

// A predecessor name that is not one path element cannot steer the path: the
// message falls back to the generic line.
func TestSuccessorHandoffPathFallsBackOnAnUnsafeName(t *testing.T) {
	t.Parallel()
	ctx := successorCtx("claude", "")
	ctx.Session.Predecessor = "acme/.."
	ctx.Role.Shift = &api.ShiftPolicy{Handoff: "/var/handoff/{session}.md"}
	result, err := (&Claude{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	last := lastWord(t, result.Command)
	if strings.Contains(last, "/var/handoff") {
		t.Errorf("an unsafe predecessor name reached the path: %s", last)
	}
	if !strings.Contains(last, "if your process names one") {
		t.Errorf("no generic fallback line: %s", last)
	}
}

func TestSuccessorFirstMessageEndsWithTheInboxLineOnlyOnABus(t *testing.T) {
	t.Parallel()
	onBus := successorCtx("claude", "")
	onBus.BusURL = "nats://127.0.0.1:4222"
	result, err := (&Claude{}).Prepare(onBus)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if last := lastWord(t, result.Command); !strings.HasSuffix(last, " Then drain your inbox.'") {
		t.Errorf("bus seat's message does not end with the inbox line: %s", last)
	}

	result, err = (&Claude{}).Prepare(successorCtx("claude", ""))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if last := lastWord(t, result.Command); strings.Contains(last, "inbox") {
		t.Errorf("a seat marvel did not attach to a bus got the inbox line: %s", last)
	}
}

func TestSuccessorMessageComesBeforeTheRolePrompt(t *testing.T) {
	t.Parallel()
	const rolePrompt = "Then triage the queue."
	ctx := successorCtx("claude", rolePrompt)
	ctx.BusURL = "nats://127.0.0.1:4222"
	result, err := (&Claude{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	last := lastWord(t, result.Command)
	succ := strings.Index(last, "You are a successor")
	inbox := strings.Index(last, "drain your inbox")
	role := strings.Index(last, rolePrompt)
	if succ < 0 || inbox < 0 || role < 0 || succ >= inbox || inbox >= role {
		t.Errorf("order is not successor, inbox, role prompt (%d, %d, %d): %s", succ, inbox, role, last)
	}
	if !strings.HasSuffix(last, rolePrompt+"'") {
		t.Errorf("the role prompt is not last: %s", last)
	}
}

func TestSuccessorPromptOffLeavesOnlyTheRolePrompt(t *testing.T) {
	t.Parallel()
	off := false
	ctx := successorCtx("claude", "just the role prompt")
	ctx.Session.Runtime.SuccessorPrompt = &off
	result, err := (&Claude{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !strings.HasSuffix(result.Command, " "+shellQuote("just the role prompt")) {
		t.Errorf("opted-out successor did not get exactly the role prompt: %s", result.Command)
	}
	if strings.Contains(result.Command, "successor") {
		t.Errorf("opted-out successor still got the successor line: %s", result.Command)
	}

	ctx = successorCtx("claude", "")
	ctx.Session.Runtime.SuccessorPrompt = &off
	result, err = (&Claude{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if strings.Contains(result.Command, "successor") || strings.HasSuffix(result.Command, " ''") {
		t.Errorf("opted out with no role prompt, yet the command carries a message: %s", result.Command)
	}
}

func TestFirstSpawnGetsOnlyTheRolePrompt(t *testing.T) {
	t.Parallel()
	ctx := successorCtx("claude", "just the role prompt")
	ctx.Session.Predecessor = ""
	result, err := (&Claude{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !strings.HasSuffix(result.Command, " "+shellQuote("just the role prompt")) || strings.Contains(result.Command, "successor") {
		t.Errorf("a first spawn did not get exactly the role prompt: %s", result.Command)
	}
}

// A wrapper owns its command line (D4): it gets the composed message in the
// environment, successor line included, and no positional.
func TestWrapperGetsTheComposedSuccessorMessageInTheEnvironment(t *testing.T) {
	t.Parallel()
	ctx := successorCtx("/usr/local/bin/seat-launch.sh", "role words")
	result, err := (&Claude{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	got := result.Env[firstPromptEnv]
	if !strings.Contains(got, "You are a successor to "+predecessorKey+".") || !strings.HasSuffix(got, "role words") {
		t.Errorf("%s = %q, want the successor line then the role prompt", firstPromptEnv, got)
	}
	if strings.Contains(result.Command, "successor") {
		t.Errorf("wrapper command carries the message: %s", result.Command)
	}
}

func TestComposeFirstPromptHeadlessIsUntouched(t *testing.T) {
	t.Parallel()
	ctx := successorCtx("claude", "summarize the repo")
	ctx.Session.Runtime.Mode = api.RuntimeModeHeadless
	result, err := (&Claude{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if strings.Contains(result.Command, "successor") || !strings.HasSuffix(result.Command, " "+shellQuote("summarize the repo")) {
		t.Errorf("headless launch changed: %s", result.Command)
	}
}
