package runtime

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// The block is written with ordered encoding: a Go map would sort "*" after the
// letters and opencode reads the last match, so "*" must come first. The data
// home is carried as given, never passed through realpath, so /var stays /var.
func TestOpenCodeConfigGoldenBytes(t *testing.T) {
	cases := []struct{ name, home, want string }{
		{
			"plain",
			"/srv/seat/data",
			`{"*":"deny","read":{"*":"allow","*.env":"deny","*.env.*":"deny","*.env.example":"allow"},"glob":"allow","grep":"allow","edit":{"*":"allow",".git/**":"deny","**/opencode.json":"deny",".opencode/**":"deny"},"external_directory":{"*":"deny","/srv/seat/data/opencode/tool-output/*":"deny"}}`,
		},
		{
			"the glob follows the input and keeps /var",
			"/var/folders/x/seat/data",
			`{"*":"deny","read":{"*":"allow","*.env":"deny","*.env.*":"deny","*.env.example":"allow"},"glob":"allow","grep":"allow","edit":{"*":"allow",".git/**":"deny","**/opencode.json":"deny",".opencode/**":"deny"},"external_directory":{"*":"deny","/var/folders/x/seat/data/opencode/tool-output/*":"deny"}}`,
		},
		{
			"a trailing slash is joined as path.Join joins",
			"/srv/seat/data/",
			`{"*":"deny","read":{"*":"allow","*.env":"deny","*.env.*":"deny","*.env.example":"allow"},"glob":"allow","grep":"allow","edit":{"*":"allow",".git/**":"deny","**/opencode.json":"deny",".opencode/**":"deny"},"external_directory":{"*":"deny","/srv/seat/data/opencode/tool-output/*":"deny"}}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, reason := openCodePermissionBlock(c.home)
			if reason != "" {
				t.Fatalf("reason = %q, want none", reason)
			}
			if string(got) != c.want {
				t.Errorf("block =\n%s\nwant\n%s", got, c.want)
			}
		})
	}
}

// A path that is not absolute reads as no home, as L2 reads it: a headless seat
// that is to have a private home and has none is refused with the same reason
// the builder returns for the same input.
func TestOpenCodeConfigRelativeDataHomeAgreesWithL2(t *testing.T) {
	for _, home := range []string{"", "data", "./data", "../data"} {
		got, reason := openCodePermissionBlock(home)
		if reason != openCodeNoPrivateHome || got != nil {
			t.Errorf("block(%q) = %q, %q; want no bytes and %q", home, got, reason, openCodeNoPrivateHome)
		}
		ctx := openCodeCtx(t, api.RuntimeModeHeadless, "ollama", "http://127.0.0.1:11434", home)
		res, err := (&OpenCode{}).Prepare(ctx)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if !strings.Contains(runStub(t, res.Command), reason) {
			t.Errorf("L2 refuses home %q with a reason other than %q", home, reason)
		}
	}
}

// runStub runs a refusal stub and returns its output, requiring exit 78.
func runStub(t *testing.T, command string) string {
	t.Helper()
	// Run nothing but a refusal stub: an unrefused launch is the real opencode.
	if !strings.HasPrefix(command, "/bin/sh -c ") || !strings.HasSuffix(command, "exit 78'") {
		t.Fatalf("not a refusal stub, not run: %q", command)
	}
	out, err := exec.Command("sh", "-c", command).CombinedOutput()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 78 {
		t.Fatalf("exit = %v, want 78 (output %q)", err, out)
	}
	return string(out)
}

func autoCtx(t *testing.T, mode api.RuntimeMode, args ...string) *LaunchContext {
	t.Helper()
	ctx := openCodeCtx(t, mode, "", "", "")
	ctx.Session.Runtime.Args = args
	ctx.Role.Runtime = ctx.Session.Runtime
	return ctx
}

// A headless seat with --auto and neither a container nor a sandbox profile is
// refused with a stub that returns a command and exits 78. The controls change
// one thing each and launch as before.
func TestOpenCodeAutoUnsandboxedRefused(t *testing.T) {
	ctx := autoCtx(t, api.RuntimeModeHeadless, "--auto")
	res, err := (&OpenCode{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare returned an error; the refusal must be a command: %v", err)
	}
	if got := runStub(t, res.Command); got != "marvel: opencode launch refused: auto-unsandboxed\n" {
		t.Errorf("output = %q", got)
	}
	if res.Stream != nil {
		t.Errorf("a refused launch wired a stream: %+v", res.Stream)
	}

	launches := []struct {
		name  string
		mode  api.RuntimeMode
		args  []string
		place api.Placement
		prof  string
	}{
		{"a sandbox profile", api.RuntimeModeHeadless, []string{"--auto"}, "", "/tmp/p.sb"},
		{"a container placement", api.RuntimeModeHeadless, []string{"--auto"}, api.PlacementContainer, ""},
		{"no --auto", api.RuntimeModeHeadless, []string{"--model", "x"}, "", ""},
		{"interactive", api.RuntimeModeInteractive, []string{"--auto"}, "", ""},
		{"--auto=false", api.RuntimeModeHeadless, []string{"--auto=false"}, "", ""},
		{"--auto=0", api.RuntimeModeHeadless, []string{"--auto=0"}, "", ""},
		{"--auto=no", api.RuntimeModeHeadless, []string{"--auto=no"}, "", ""},
	}
	for _, c := range launches {
		t.Run(c.name, func(t *testing.T) {
			ctx := autoCtx(t, c.mode, c.args...)
			ctx.Placement, ctx.SandboxProfile = c.place, c.prof
			res, err := (&OpenCode{}).Prepare(ctx)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if strings.Contains(res.Command, "exit 78") {
				t.Errorf("launch refused: %q", res.Command)
			}
		})
	}

	// An off value after an on one does not undo it.
	if got := openCodeLaunchRefusal(autoCtx(t, api.RuntimeModeHeadless, "--auto", "--auto=false")); got != openCodeRefuseAutoUnsandboxed {
		t.Errorf("refusal for --auto then --auto=false = %q, want %q", got, openCodeRefuseAutoUnsandboxed)
	}
	// Any other value counts as on, so the refusal holds.
	for _, a := range []string{"--auto=true", "--auto=1", "--auto=yes", "--auto=", "--auto=False", "--auto=off"} {
		if got := openCodeLaunchRefusal(autoCtx(t, api.RuntimeModeHeadless, a)); got != openCodeRefuseAutoUnsandboxed {
			t.Errorf("refusal for %s = %q, want %q", a, got, openCodeRefuseAutoUnsandboxed)
		}
	}
}

// The check is pure and names the lever it found. It is not called from
// Prepare: a role env that sets a lever launches as today (G2).
func TestOpenCodeRoleEnvConflictNamesLever(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"config content", map[string]string{"OPENCODE_CONFIG_CONTENT": "{}"}, "OPENCODE_CONFIG_CONTENT"},
		{"permission", map[string]string{"OPENCODE_PERMISSION": "{}"}, "OPENCODE_PERMISSION"},
		{"both names the first in sorted order", map[string]string{"OPENCODE_PERMISSION": "{}", "OPENCODE_CONFIG_CONTENT": "{}"}, "OPENCODE_CONFIG_CONTENT"},
		{"a lever set to the empty string still counts", map[string]string{"OPENCODE_PERMISSION": ""}, "OPENCODE_PERMISSION"},
		{"unrelated keys", map[string]string{"HOME": "/x", "XDG_CONFIG_HOME": "/y"}, ""},
		{"no env", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reason, lever := openCodeRoleEnvConflict(c.env)
			if lever != c.want {
				t.Fatalf("lever = %q, want %q", lever, c.want)
			}
			wantReason := ""
			if c.want != "" {
				wantReason = openCodeRefuseRoleEnvConflict
			}
			if reason != wantReason {
				t.Errorf("reason = %q, want %q", reason, wantReason)
			}
		})
	}
}

func TestOpenCodePrepareDoesNotCallRoleEnvConflict(t *testing.T) {
	ctx := autoCtx(t, api.RuntimeModeHeadless)
	ctx.Session.Runtime.Env = map[string]string{"OPENCODE_PERMISSION": "{}"}
	res, err := (&OpenCode{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if strings.Contains(res.Command, "exit 78") {
		t.Errorf("a role env that names a lever refused the launch: %q", res.Command)
	}
}

// The refusal reads the words of runtime.command as well as runtime.args,
// because the adapter passes a command's words to opencode too. A headless
// unsandboxed launch whose command cannot be read as plain words is refused,
// since the flag may sit where nothing here can see it.
func TestOpenCodeLaunchRefusalReadsTheCommandWords(t *testing.T) {
	cases := []struct {
		name    string
		command string
		args    []string
		place   api.Placement
		prof    string
		want    string
	}{
		{"--auto in the command", "opencode --auto", nil, "", "", openCodeRefuseAutoUnsandboxed},
		{"--auto=true in the command", "opencode --auto=true", nil, "", "", openCodeRefuseAutoUnsandboxed},
		{"--auto=false in the command", "opencode --auto=false", nil, "", "", ""},
		{"a quote splitting the flag", "opencode --au'to'", nil, "", "", openCodeRefuseUnreadableCommand},
		{"an expansion in the command", "opencode $FLAGS", nil, "", "", openCodeRefuseUnreadableCommand},
		{"a pipe in the command", "opencode | tee x", nil, "", "", openCodeRefuseUnreadableCommand},
		{"a plain command and no flag", "opencode", nil, "", "", ""},
		{"an unreadable command under a sandbox profile", "opencode --au'to'", nil, "", "/tmp/p.sb", ""},
		{"an unreadable command in a container", "opencode --au'to'", nil, api.PlacementContainer, "", ""},
		{"--auto in the command under a sandbox profile", "opencode --auto", nil, "", "/tmp/p.sb", ""},
		{"--auto in args still refuses", "opencode", []string{"--auto"}, "", "", openCodeRefuseAutoUnsandboxed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := autoCtx(t, api.RuntimeModeHeadless, c.args...)
			ctx.Session.Runtime.Command = c.command
			ctx.Placement, ctx.SandboxProfile = c.place, c.prof
			if got := openCodeLaunchRefusal(ctx); got != c.want {
				t.Fatalf("refusal = %q, want %q", got, c.want)
			}
		})
	}

	// An interactive launch is not covered, as before.
	ctx := autoCtx(t, api.RuntimeModeInteractive)
	ctx.Session.Runtime.Command = "opencode --au'to'"
	if got := openCodeLaunchRefusal(ctx); got != "" {
		t.Errorf("interactive refusal = %q, want none", got)
	}
}
