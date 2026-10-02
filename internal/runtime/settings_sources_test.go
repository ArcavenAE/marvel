package runtime

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// A bare claude launch tells the harness which settings sources to load, so
// what applies is a declaration and not a consequence of the directory
// (docs/design/seat-bootstrap.md section 4, SB-2). A wrapper owns its own
// command line and gets nothing from marvel.

func claudeCtx(command string) *LaunchContext {
	ctx := testContext()
	ctx.Session.Runtime.Name = "claude"
	ctx.Session.Runtime.Command = command
	ctx.Session.Runtime.Args = nil
	return ctx
}

// A role that declares nothing keeps today's behavior: every source, so a seat
// that relies on its directory's settings and CLAUDE.md is not changed by the
// upgrade. The placed defaults (design section 4: user,project for a declared
// workdir, user for a managed one) arrive with SB-1, where a session first has a
// placement to tell apart.
func TestClaudeSettingSourcesDefaults(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		declared []string
		want     string
	}{
		{"an undeclared role keeps every source", nil, "user,project,local"},
		{"a declaration narrows it", []string{"user"}, "user"},
		{"a declaration wins over the default", []string{"project"}, "project"},
	}
	for _, tc := range cases {
		ctx := claudeCtx("claude")
		ctx.Role.SettingsSources = tc.declared
		result, err := (&Claude{}).Prepare(ctx)
		if err != nil {
			t.Fatalf("%s: Prepare: %v", tc.name, err)
		}
		if !strings.Contains(result.Command, "--setting-sources "+tc.want) || strings.Count(result.Command, "--setting-sources") != 1 {
			t.Errorf("%s: command %q, want exactly one --setting-sources %s", tc.name, result.Command, tc.want)
		}
		if result.SettingSources != tc.want {
			t.Errorf("%s: result.SettingSources = %q, want %q", tc.name, result.SettingSources, tc.want)
		}
	}
}

func TestClaudeSettingSourcesNotAddedForAWrapper(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"/home/op/.marvel/manifests/cast-seat.sh", "docker run --rm img claude", "npx claude"} {
		result, err := (&Claude{}).Prepare(claudeCtx(command))
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if strings.Contains(result.Command, "--setting-sources") || result.SettingSources != "" {
			t.Errorf("wrapper %q: command %q, sources %q, want none from marvel", command, result.Command, result.SettingSources)
		}
	}
}

// A role whose own args name the sources keeps them, in either form.
func TestClaudeSettingSourcesKeepsTheRolesOwn(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"--setting-sources", "local"}, {"--setting-sources=local"}} {
		ctx := claudeCtx("claude")
		ctx.Session.Runtime.Args = args
		result, err := (&Claude{}).Prepare(ctx)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if got := strings.Count(result.Command, "--setting-sources"); got != 1 {
			t.Errorf("args %v: %d --setting-sources in %q, want the role's one", args, got, result.Command)
		}
		if result.SettingSources != "" {
			t.Errorf("args %v: result.SettingSources = %q, want empty: marvel passed none", args, result.SettingSources)
		}
	}
}

func TestClaudeSettingSourcesOnAHeadlessRun(t *testing.T) {
	t.Parallel()
	ctx := claudeCtx("claude")
	ctx.Session.Runtime.Mode = api.RuntimeModeHeadless
	ctx.Session.Runtime.Prompt = "do the thing"
	result, err := (&Claude{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !strings.Contains(result.Command, "--setting-sources user") {
		t.Errorf("headless command %q, want --setting-sources user", result.Command)
	}
}

// A flag written inside the command string is the role's own, as one in args is:
// marvel adds none, so the command line never carries two.
func TestClaudeSettingSourcesKeepsOneEmbeddedInTheCommand(t *testing.T) {
	t.Parallel()
	for _, command := range []string{
		"claude --setting-sources project",
		"/usr/local/bin/claude --setting-sources=project",
	} {
		ctx := claudeCtx(command)
		result, err := (&Claude{}).Prepare(ctx)
		if err != nil {
			t.Fatalf("%q: %v", command, err)
		}
		if n := strings.Count(result.Command, "--setting-sources"); n != 1 {
			t.Errorf("%q: %d --setting-sources in %q, want the role's one", command, n, result.Command)
		}
		if result.SettingSources != "" {
			t.Errorf("%q: recorded %q, want none: marvel passed nothing", command, result.SettingSources)
		}
	}
}
