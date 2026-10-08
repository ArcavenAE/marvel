package runtime

import (
	"fmt"
	"strings"
	"testing"
)

// marvel#745: a role that keeps its own system prompt inline in its command
// keeps it. The prompt site and the setting-sources site read the same two
// places, runtime.args and the command's own arguments, so they cannot drift.

func prepareClaude(t *testing.T, command string, args []string) *LaunchResult {
	t.Helper()
	ctx := claudeCtx(command)
	ctx.Session.Runtime.Args = args
	result, err := (&Claude{}).Prepare(ctx)
	if err != nil {
		t.Fatalf("Prepare(%q): %v", command, err)
	}
	return result
}

func TestClaudeKeepsAnInlineSystemPrompt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		command string
		args    []string
		want    int  // occurrences of --append-system-prompt, the -file form included
		injects bool // marvel's identity line is on the command
	}{
		{"plain claude is injected", "claude", nil, 1, true},
		{"inline flag with a value", "claude --append-system-prompt mine", nil, 1, false},
		{"inline joined form", "claude --append-system-prompt=mine", nil, 1, false},
		{"inline file flag", "claude --append-system-prompt-file f", nil, 1, false},
		{"inline file flag, joined", "claude --append-system-prompt-file=f", nil, 1, false},
		{"inline flag among others", "claude --model sonnet --append-system-prompt mine --verbose", nil, 1, false},
		{"flag in args", "claude", []string{"--append-system-prompt", "mine"}, 1, false},
		{"file flag in args", "claude", []string{"--append-system-prompt-file", "f"}, 1, false},
		{"unrelated inline flags still get the prompt", "claude --model sonnet", nil, 1, true},
		{"an unrelated joined flag still gets the prompt", "claude --model=x", nil, 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := prepareClaude(t, tc.command, tc.args)
			if got := strings.Count(result.Command, "--append-system-prompt"); got != tc.want {
				t.Errorf("--append-system-prompt count = %d, want %d in: %s", got, tc.want, result.Command)
			}
			// The injected line carries the session identity; the role's own
			// prompt does not get it appended, and does not lose its flag.
			if injected := strings.Contains(result.Command, "You are squad-worker-g1-0"); injected != tc.injects {
				t.Errorf("identity line present = %v, want %v in: %s", injected, tc.injects, result.Command)
			}
		})
	}
}

// Both prompt flags and the setting-sources flag are read from both places.
func TestClaudeReadsInlineAndArgFlagsAtBothSites(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"--append-system-prompt", "--append-system-prompt-file", "--setting-sources"} {
		for _, where := range []string{"command", "args"} {
			command, args := "claude", []string(nil)
			if where == "command" {
				command = "claude " + flag + " x"
			} else {
				args = []string{flag, "x"}
			}
			result := prepareClaude(t, command, args)
			if flag == "--setting-sources" {
				if got := strings.Count(result.Command, "--setting-sources"); got != 1 || result.SettingSources != "" {
					t.Errorf("%s in %s: %d in %q, sources %q, want the role's one and none from marvel", flag, where, got, result.Command, result.SettingSources)
				}
				continue
			}
			if got := strings.Count(result.Command, "--append-system-prompt"); got != 1 {
				t.Errorf("%s in %s: %d prompt flags in %q, want the role's one", flag, where, got, result.Command)
			}
		}
	}
}

// A command whose words the adapter cannot read is shell text: the harness may
// get a prompt flag from an expansion, a pipe or a second line, so marvel adds
// none and says so once. The setting-sources line is unchanged.
func TestClaudeAddsNoPromptToShellText(t *testing.T) {
	cases := []string{
		"claude $(cat flags)",
		"claude `cat flags`",
		"claude ${FLAGS}",
		"claude --x | tee out",
		"claude --x ; true",
		"claude --x &",
		"claude < in",
		"claude > out",
		"claude (x)",
		"claude {x}",
		`claude --model "sonnet 4"`,
		`claude --model 'a b'`,
		`claude --append-system-prompt="x y"`,
		`claude --append-system-prompt-file='f g'`,
		"claude --add-dir ~/x",
		"claude --add-dir *.d",
		"claude --x # --append-system-prompt y",
		`claude \--append-system-prompt y`,
		"claude --x\n--append-system-prompt y",
		"claude --x",
		"claude --x\r--y",
	}
	for _, command := range cases {
		var lines []string
		old := logLaunch
		logLaunch = func(format string, v ...any) { lines = append(lines, fmt.Sprintf(format, v...)) }
		result := prepareClaude(t, command, nil)
		logLaunch = old
		if strings.Contains(result.Command, "You are squad-worker-g1-0") {
			t.Errorf("%q: a prompt was injected into shell text: %s", command, result.Command)
		}
		want := "role worker: command is shell text; marvel's system-prompt and setting-sources flags not added"
		if len(lines) != 1 || lines[0] != want {
			t.Errorf("%q: log lines = %q, want exactly %q", command, lines, want)
		}
		// Nothing is declared, so marvel passes no sources either: a flag it
		// cannot see may already be there, and a second is not harmless.
		if strings.Contains(result.Command, "--setting-sources") || result.SettingSources != "" {
			t.Errorf("%q: marvel added setting-sources to shell text: %q, sources %q", command, result.Command, result.SettingSources)
		}
		if !strings.Contains(result.Command, "--permission-mode plan") {
			t.Errorf("%q: the rest of the launch changed: %s", command, result.Command)
		}
	}
}

// The line is for the decision shell text made, not for every launch.
func TestClaudeLogsNothingWhenThePromptWasNotInTheWay(t *testing.T) {
	for _, command := range []string{
		"claude",
		"claude --append-system-prompt mine",
		"claude --model=x",
		"claude --append-system-prompt=x",
		"FOO=1 claude --model x",

		"npx claude",
		"docker run --rm -it img claude",
		"/home/op/.marvel/manifests/cast-seat.sh",
		"FOO=1 claude",
	} {
		var lines []string
		old := logLaunch
		logLaunch = func(format string, v ...any) { lines = append(lines, fmt.Sprintf(format, v...)) }
		prepareClaude(t, command, nil)
		logLaunch = old
		if len(lines) != 0 {
			t.Errorf("%q: unexpected log lines %q", command, lines)
		}
	}
}

// Quotes can hide a flag from a reader of the words: the shell strips them and
// the harness sees the real flag, so a quoted spelling is shell text and marvel
// adds no prompt of its own (marvel#745).
func TestClaudeAddsNoPromptWhenAFlagIsQuoted(t *testing.T) {
	for _, flag := range []string{"--append-system-prompt", "--append-system-prompt-file", "--setting-sources"} {
		half := len(flag) / 2
		for name, spelled := range map[string]string{
			"double-quoted":    `"` + flag + `"`,
			"single-quoted":    `'` + flag + `'`,
			"split by quotes":  flag[:half] + `""` + flag[half:],
			"split by singles": flag[:half] + `''` + flag[half:],
			"escaped inside":   flag[:half] + `\` + flag[half:],
		} {
			var lines []string
			old := logLaunch
			logLaunch = func(format string, v ...any) { lines = append(lines, fmt.Sprintf(format, v...)) }
			result := prepareClaude(t, "claude "+spelled+" x", nil)
			logLaunch = old
			if strings.Contains(result.Command, "You are squad-worker-g1-0") {
				t.Errorf("%s %s: marvel's prompt was added beside a flag the shell would read: %s", flag, name, result.Command)
			}
			want := "role worker: command is shell text; marvel's system-prompt and setting-sources flags not added"
			if len(lines) != 1 || lines[0] != want {
				t.Errorf("%s %s: log lines = %q, want exactly %q", flag, name, lines, want)
			}
			if result.SettingSources != "" {
				t.Errorf("%s %s: marvel passed sources %q beside a flag it cannot read", flag, name, result.SettingSources)
			}
			if strings.Count(result.Command, "--setting-sources") > 1 {
				t.Errorf("%s %s: --setting-sources appears twice: %s", flag, name, result.Command)
			}
			if !strings.Contains(result.Command, "--permission-mode plan") {
				t.Errorf("%s %s: the rest of the launch changed: %s", flag, name, result.Command)
			}
		}
	}
}

// A role that declares settings_sources keeps them even on a command marvel
// cannot read: the flag is appended, and the log line says only that, because
// whether claude receives it depends on the command (a pipe, a semicolon or a
// comment can leave it on another program or inside the comment; marvel#745).
func TestClaudeKeepsDeclaredSourcesOnShellText(t *testing.T) {
	for _, command := range []string{`claude "--setting-sources" user`, "claude $(cat flags)", `claude '--setting-sources' user`} {
		var lines []string
		old := logLaunch
		logLaunch = func(format string, v ...any) { lines = append(lines, fmt.Sprintf(format, v...)) }
		ctx := claudeCtx(command)
		ctx.Role.SettingsSources = []string{"project"}
		result, err := (&Claude{}).Prepare(ctx)
		logLaunch = old
		if err != nil {
			t.Fatalf("Prepare(%q): %v", command, err)
		}
		if !strings.HasSuffix(result.Command, " --setting-sources project") || result.SettingSources != "project" {
			t.Errorf("%q: the declared sources were not passed last: %q, sources %q", command, result.Command, result.SettingSources)
		}
		if strings.Contains(result.Command, "You are squad-worker-g1-0") {
			t.Errorf("%q: a prompt was added to shell text: %s", command, result.Command)
		}
		wantLines := []string{
			"role worker: command is shell text; marvel's system-prompt line not added",
			"role worker: command is shell text; marvel appended --setting-sources project from the manifest; whether claude receives it depends on the command",
		}
		if len(lines) != 2 || lines[0] != wantLines[0] || lines[1] != wantLines[1] {
			t.Errorf("%q: log lines = %q, want %q", command, lines, wantLines)
		}
	}
}

// Readable commands keep today's setting-sources behavior.
func TestClaudeSourcesOnReadableCommandsAreUnchanged(t *testing.T) {
	t.Parallel()
	result := prepareClaude(t, "claude", nil)
	if result.SettingSources != "user,project,local" || strings.Count(result.Command, "--setting-sources user,project,local") != 1 {
		t.Errorf("plain claude: %q, sources %q, want user,project,local injected", result.Command, result.SettingSources)
	}
	result = prepareClaude(t, "claude --setting-sources user", nil)
	if result.SettingSources != "" || strings.Count(result.Command, "--setting-sources") != 1 {
		t.Errorf("readable inline flag: %q, sources %q, want the role's one and none from marvel", result.Command, result.SettingSources)
	}
}

// The measured commands where the appended flag may not reach claude: the line
// states what marvel did and not what claude will do, and never says "wins".
func TestClaudeSourcesLineClaimsOnlyWhatMarvelDid(t *testing.T) {
	for _, command := range []string{"claude --x | tee out", "claude --x ; true", "claude --x # note"} {
		var lines []string
		old := logLaunch
		logLaunch = func(format string, v ...any) { lines = append(lines, fmt.Sprintf(format, v...)) }
		ctx := claudeCtx(command)
		ctx.Role.SettingsSources = []string{"project"}
		result, err := (&Claude{}).Prepare(ctx)
		logLaunch = old
		if err != nil {
			t.Fatalf("Prepare(%q): %v", command, err)
		}
		if !strings.HasSuffix(result.Command, " --setting-sources project") || result.SettingSources != "project" {
			t.Errorf("%q: the flag was not appended: %q, sources %q", command, result.Command, result.SettingSources)
		}
		want := "role worker: command is shell text; marvel appended --setting-sources project from the manifest; whether claude receives it depends on the command"
		if len(lines) != 2 || lines[1] != want {
			t.Errorf("%q: log lines = %q, want the second to be %q", command, lines, want)
		}
		for _, l := range lines {
			if strings.Contains(l, "wins") {
				t.Errorf("%q: a log line claims the flag wins: %q", command, l)
			}
		}
	}
}

// A readable command with a declaration is unchanged and logs nothing.
func TestClaudeReadableCommandWithDeclaredSourcesLogsNothing(t *testing.T) {
	var lines []string
	old := logLaunch
	logLaunch = func(format string, v ...any) { lines = append(lines, fmt.Sprintf(format, v...)) }
	ctx := claudeCtx("claude --model sonnet")
	ctx.Role.SettingsSources = []string{"project", "local"}
	result, err := (&Claude{}).Prepare(ctx)
	logLaunch = old
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(result.Command, " --setting-sources project,local") || result.SettingSources != "project,local" {
		t.Errorf("readable command: %q, sources %q, want the declaration appended", result.Command, result.SettingSources)
	}
	if len(lines) != 0 {
		t.Errorf("a readable command logged %q", lines)
	}
}

// With several declared sources on shell text, the log line names all of them,
// in the manifest's order, and not only the first (marvel#745).
func TestClaudeSourcesLineNamesEveryDeclaredSource(t *testing.T) {
	var lines []string
	old := logLaunch
	logLaunch = func(format string, v ...any) { lines = append(lines, fmt.Sprintf(format, v...)) }
	ctx := claudeCtx("claude --x | tee out")
	ctx.Role.SettingsSources = []string{"project", "local"}
	result, err := (&Claude{}).Prepare(ctx)
	logLaunch = old
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(result.Command, " --setting-sources project,local") || result.SettingSources != "project,local" {
		t.Errorf("the flag was not appended whole: %q, sources %q", result.Command, result.SettingSources)
	}
	want := "role worker: command is shell text; marvel appended --setting-sources project,local from the manifest; whether claude receives it depends on the command"
	if len(lines) != 2 || lines[1] != want {
		t.Errorf("log lines = %q, want the second to be %q", lines, want)
	}
}
