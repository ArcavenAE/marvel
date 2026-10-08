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
		{"a quoted argument is not shell text", `claude --model "sonnet 4"`, nil, 1, true},
		{"a single-quoted argument is not shell text", `claude --model 'sonnet'`, nil, 1, true},
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
		if strings.Contains(result.Command, "--append-system-prompt") {
			t.Errorf("%q: a prompt was injected into shell text: %s", command, result.Command)
		}
		want := "role worker: command is shell text; marvel's system-prompt line not added"
		if len(lines) != 1 || lines[0] != want {
			t.Errorf("%q: log lines = %q, want exactly %q", command, lines, want)
		}
		if got := strings.Count(result.Command, "--setting-sources"); got != 1 || result.SettingSources != "user,project,local" {
			t.Errorf("%q: setting-sources changed: %d in %q, sources %q", command, got, result.Command, result.SettingSources)
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
		`claude --model "sonnet 4"`,
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
