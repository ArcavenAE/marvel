package runtime

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// A scheduled run has no human at a prompt, so the launch holds it to what
// apply already holds it to, on the argv the launch actually builds
// (aae-orc-iyun3 part a). A Prepare error would not do: the manager answers one
// with the raw command, so the refusal is a stub that exits 78, as the goose
// adapter's is.

// scheduledClaudeCtx is a headless claude launch on a scheduled role with the
// given command, args and permission mode.
func scheduledClaudeCtx(command string, args []string, permissions string) *LaunchContext {
	ctx := testContext()
	ctx.Session.Runtime = api.Runtime{
		Name: "claude", Command: command, Args: args,
		Mode: api.RuntimeModeHeadless, Prompt: "summarize",
	}
	ctx.Role.Runtime = ctx.Session.Runtime
	ctx.Role.Permissions = permissions
	ctx.Role.Schedule = &api.SchedulePolicy{Cron: "0 6 * * *", Timezone: "Etc/UTC"}
	return ctx
}

// isRefusalStub reports whether the launch is the exit-78 stub and not claude.
func isRefusalStub(cmd string) bool {
	return strings.Contains(cmd, "exit 78") && !strings.HasPrefix(cmd, "claude")
}

func TestClaudeScheduledLaunchRefusesWhatApplyRefuses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ctx  *LaunchContext
	}{
		{"a bypass flag in the args", scheduledClaudeCtx("claude", []string{"--dangerously-skip-permissions"}, "")},
		{"the bypass permission mode on the role", scheduledClaudeCtx("claude", nil, "bypassPermissions")},
		{"a command in shell form", scheduledClaudeCtx("claude --model 'x'", nil, "")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := (&Claude{}).Prepare(tc.ctx)
			if err != nil {
				t.Fatalf("Prepare: %v, want the refusal stub and no error (an error falls back to the raw command)", err)
			}
			if !isRefusalStub(result.Command) {
				t.Fatalf("command = %q, want the exit-78 stub: a scheduled launch must not carry what apply refuses", result.Command)
			}
			if strings.Contains(result.Command, "--dangerously-skip-permissions") {
				t.Errorf("command = %q, want no bypass flag in the refusal", result.Command)
			}
		})
	}
}

// What apply allows still launches, and a role with no schedule is unchanged.
// These guards pass before the change.
func TestClaudeScheduledLaunchStillStartsAnAllowedRole(t *testing.T) {
	t.Parallel()

	allowed := scheduledClaudeCtx("claude", []string{"--model", "sonnet"}, "dontAsk")
	result, err := (&Claude{}).Prepare(allowed)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if isRefusalStub(result.Command) || !strings.HasPrefix(result.Command, "claude") {
		t.Errorf("command = %q, want claude launched for an allowed scheduled role", result.Command)
	}

	unscheduled := scheduledClaudeCtx("claude", []string{"--dangerously-skip-permissions"}, "")
	unscheduled.Role.Schedule = nil
	result, err = (&Claude{}).Prepare(unscheduled)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if isRefusalStub(result.Command) {
		t.Errorf("command = %q, want an unscheduled role left to its own rules", result.Command)
	}
}
