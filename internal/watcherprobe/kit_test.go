package watcherprobe

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

const kitScript = "../../scripts/probes/watcher-claude/run.sh"

// runKit runs the kit with a clean environment plus extra, so a test never
// inherits the seat's own MARVEL_* or credentials. A dry run launches nothing.
func runKit(t *testing.T, extra []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{kitScript}, args...)...)
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}, extra...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestKitDryRunPlacesEverythingUnderTheScratchDirectory(t *testing.T) {
	scratch := filepath.Join(t.TempDir(), "probe")
	out, err := runKit(t, []string{"MARVEL_SOCKET=/live/marvel.sock", "TMUX=/live/tmux,1,0", "MARVEL_TMUX_SOCKET=/live/tmux"}, "--dry-run", "--scratch", scratch)
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	for _, want := range []string{
		"HOME=" + scratch + "/home",
		"CLAUDE_CONFIG_DIR=" + scratch + "/config",
		"TMUX_SOCKET=" + scratch + "/tmux.sock",
		"REPO=" + scratch + "/repo",
		"EVENTS=" + scratch + "/events.tsv",
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("dry run does not print %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "/live") {
		t.Errorf("a live path reached the kit's environment:\n%s", out)
	}
	if _, err := os.Stat(scratch); err == nil {
		t.Error("a dry run created the scratch directory")
	}
}

func TestKitRefusesWhatWouldTouchALiveConfig(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	full := t.TempDir()
	if err := os.WriteFile(filepath.Join(full, "x"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// A symlinked component into the real config tree passes a text comparison.
	links := t.TempDir()
	if err := os.Symlink(filepath.Join(home, ".claude"), filepath.Join(links, "cfg")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(home, filepath.Join(links, "home")); err != nil {
		t.Fatal(err)
	}
	pw, err := user.Current()
	if err != nil || pw.HomeDir == "" {
		t.Skipf("no passwd home: %v", err)
	}
	for name, tc := range map[string]struct {
		extra []string
		args  []string
		path  string // PATH for this run; empty keeps the caller's
		msg   string
	}{
		"a scratch inside the real config":       {[]string{"HOME=" + home}, []string{"--dry-run", "--scratch", filepath.Join(home, ".claude", "probe")}, "", "is inside"},
		"a scratch reached through a symlink":    {[]string{"HOME=" + home}, []string{"--dry-run", "--scratch", filepath.Join(links, "cfg", "probe")}, "", "is inside"},
		"a scratch under a symlink to the home":  {[]string{"HOME=" + home}, []string{"--dry-run", "--scratch", filepath.Join(links, "home", ".claude", "probe")}, "", "is inside"},
		"an empty HOME":                          {[]string{"HOME="}, []string{"--dry-run", "--scratch", filepath.Join(t.TempDir(), "probe")}, "", "HOME is empty"},
		"an empty HOME and the real config":      {[]string{"HOME="}, []string{"--dry-run", "--scratch", filepath.Join(pw.HomeDir, ".claude", "probe-test-only")}, "", "is inside"},
		"the passwd home when HOME says another": {[]string{"HOME=" + home}, []string{"--dry-run", "--scratch", filepath.Join(pw.HomeDir, ".claude", "probe-test-only")}, "", "is inside"},
		"a scratch that is not empty":            {nil, []string{"--dry-run", "--scratch", full}, "", "is not empty"},
		"a relative scratch":                     {nil, []string{"--dry-run", "--scratch", "probe"}, "", "absolute"},
		"a real run with no credential":          {nil, []string{"--scratch", filepath.Join(t.TempDir(), "probe")}, "/usr/bin:/bin", "WATCHER_PROBE_CREDENTIAL is required"},
	} {
		extra := tc.extra
		if tc.path != "" {
			extra = append(extra, "PATH="+tc.path)
		}
		out, err := runKit(t, extra, tc.args...)
		if err == nil {
			t.Errorf("%s: the kit ran:\n%s", name, out)
			continue
		}
		if !strings.Contains(out, tc.msg) {
			t.Errorf("%s: refused, but not for the expected reason %q:\n%s", name, tc.msg, out)
		}
	}
}

func TestKitNeverNamesTheOperatorsClaudeConfig(t *testing.T) {
	dir := filepath.Dir(kitScript)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"~/.claude", "$HOME/.claude", "${HOME}/.claude", ".claude.json"} {
			if strings.Contains(string(b), bad) && e.Name() != "run.sh" {
				t.Errorf("%s names %q", e.Name(), bad)
			}
		}
	}
}

// A tmux call without -S would reach the default server, which is the fleet's.
func TestKitNamesItsOwnSocketOnEveryTmuxCall(t *testing.T) {
	b, err := os.ReadFile(kitScript)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	for i, line := range strings.Split(string(b), "\n") {
		code, _, _ := strings.Cut(line, "#")
		idx := strings.Index(code, "tmux")
		for idx >= 0 {
			rest := code[idx+len("tmux"):]
			before := ""
			if idx > 0 {
				before = code[idx-1 : idx]
			}
			// A word on its own, such as the command: not part of a name like TMUX_SOCKET.
			if (before == "" || before == " " || before == "\t" || before == "(" || before == "=") && (rest == "" || rest[0] == ' ') {
				calls++
				if !strings.HasPrefix(rest, ` -S "$sock"`) {
					t.Errorf("run.sh:%d calls tmux without -S \"$sock\": %s", i+1, strings.TrimSpace(line))
				}
			}
			next := strings.Index(rest, "tmux")
			if next < 0 {
				break
			}
			idx += len("tmux") + next
		}
	}
	if calls == 0 {
		t.Error("found no tmux call; the check reads nothing")
	}
}

func TestKitDryRunNeverPrintsTheCredential(t *testing.T) {
	out, err := runKit(t, []string{"WATCHER_PROBE_CREDENTIAL=sk-sekret-1234"}, "--dry-run", "--scratch", filepath.Join(t.TempDir(), "probe"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "sekret") || !strings.Contains(out, "CREDENTIAL=supplied\n") {
		t.Errorf("dry run output:\n%s", out)
	}
}

const fakeKey = "sk-ant-api03-FAKEFAKEFAKE0123456789abcdefXYZ"

func buildTool(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "watcherprobe")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/watcherprobe").CombinedOutput(); err != nil {
		t.Fatalf("build the tool: %v\n%s", err, out)
	}
	return bin
}

// Every capture the kit writes goes through save_capture, which masks first, so
// no key text, whole or in part, can land in a file. The pane is planted with a
// fake key in the forms claude's key dialog is reported to show.
func TestKitSaveCaptureMasksKeysAndFragments(t *testing.T) {
	tool := buildTool(t)
	dir := t.TempDir()
	pane := strings.Join([]string{
		"Detected a custom API key in your environment",
		"ANTHROPIC_API_KEY: sk-ant-...0123456789abcdefXYZ",
		"full: " + fakeKey,
		"tail only: 0123456789abcdefXYZ",
		"an ordinary line with task-queue and sk in it",
	}, "\n") + "\n"
	out := filepath.Join(dir, "capture.txt")
	cmd := exec.Command("bash", "-c", `tool=$1; cred=$2; source "$3"; save_capture "$4"`, "bash", tool, fakeKey, filepath.Join(filepath.Dir(kitScript), "lib.sh"), out)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	cmd.Stdin = strings.NewReader(pane)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("save_capture: %v\n%s", err, b)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"FAKEFAKE", "0123456789abcdef", "sk-ant-", "XYZ"} {
		if strings.Contains(string(got), leak) {
			t.Errorf("the capture still holds %q:\n%s", leak, got)
		}
	}
	for _, keep := range []string{"Detected a custom API key", "task-queue and sk in it"} {
		if !strings.Contains(string(got), keep) {
			t.Errorf("the capture lost ordinary text %q:\n%s", keep, got)
		}
	}
}

// A capture that skips save_capture is the leak, so the driver may read the pane
// only through pane(), and only to grep, checksum or save_capture it.
func TestKitWritesNoCaptureExceptThroughSaveCapture(t *testing.T) {
	b, err := os.ReadFile(kitScript)
	if err != nil {
		t.Fatal(err)
	}
	uses := 0
	for i, line := range strings.Split(string(b), "\n") {
		code, _, _ := strings.Cut(line, "#")
		if strings.Contains(code, "capture-pane") && !strings.HasPrefix(strings.TrimSpace(code), "pane()") {
			t.Errorf("run.sh:%d reads the pane outside pane(): %s", i+1, strings.TrimSpace(line))
		}
		trimmed := strings.TrimSpace(code)
		if strings.HasPrefix(trimmed, "pane()") || strings.HasPrefix(trimmed, "echo ") || !strings.Contains(code, "pane") {
			continue
		}
		for _, f := range strings.Fields(code) {
			if f == "pane" {
				uses++
			}
		}
		if strings.Contains(code, "pane >") || strings.Contains(code, "pane>") {
			t.Errorf("run.sh:%d writes the pane to a file unmasked: %s", i+1, trimmed)
		}
		if strings.Contains(code, "stop-capture") && !strings.Contains(code, "save_capture") {
			t.Errorf("run.sh:%d saves a capture without save_capture: %s", i+1, trimmed)
		}
	}
	if uses == 0 {
		t.Error("found no use of pane; the check reads nothing")
	}
}

func TestKitHandsTheCredentialToClaudeOnlyThroughAHelper(t *testing.T) {
	b, err := os.ReadFile(kitScript)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "ANTHROPIC_API_KEY") {
		t.Error("run.sh sets ANTHROPIC_API_KEY, which makes claude ask about a custom API key and show a piece of it; use apiKeyHelper")
	}
	if !strings.Contains(string(b), "apiKeyHelper") {
		t.Error("run.sh configures no apiKeyHelper")
	}
}
