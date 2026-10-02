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

// Without a placement concept on this base nothing is placed, so the default is
// the operator's own settings; the placed default (user,project, design section
// 4) arrives with SB-1, which is where a session first has a declared workdir.
func TestClaudeSettingSourcesDefaults(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		declared []string
		want     string
	}{
		{"default loads the operator's settings only", nil, "user"},
		{"local only by declaration", []string{"user", "project", "local"}, "user,project,local"},
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
