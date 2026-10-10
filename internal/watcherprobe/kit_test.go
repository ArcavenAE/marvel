package watcherprobe

import (
	"os"
	"os/exec"
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
	for name, tc := range map[string]struct {
		extra []string
		args  []string
	}{
		"a scratch inside the real config": {[]string{"HOME=" + home}, []string{"--dry-run", "--scratch", filepath.Join(home, ".claude", "probe")}},
		"a scratch that is not empty":      {nil, []string{"--dry-run", "--scratch", full}},
		"a relative scratch":               {nil, []string{"--dry-run", "--scratch", "probe"}},
		"a real run with no credential":    {nil, []string{"--scratch", filepath.Join(t.TempDir(), "probe")}},
	} {
		out, err := runKit(t, tc.extra, tc.args...)
		if err == nil {
			t.Errorf("%s: the kit ran:\n%s", name, out)
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
