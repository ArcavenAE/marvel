package tmux

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/timing"
)

// stubTmux puts a tmux that answers with success after delay first on PATH, so
// the real Driver runs against it and the real tmux server is never reached.
func stubTmux(t *testing.T, delay string) {
	t.Helper()
	dir := t.TempDir()
	body := "#!/bin/sh\nsleep " + delay + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(SocketEnv, "timing-"+filepath.Base(dir))
}

func TestDriverRecordsEachSubcommand(t *testing.T) {
	stubTmux(t, "0.05")
	d, err := NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	reg := timing.NewRegistry(nil, time.Now)
	d.SetTiming(reg)
	d.HasSession("s1")
	d.HasSession("s2")
	if _, err := d.ListSessions(); err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	sites := map[string]timing.SiteReport{}
	for _, s := range reg.Report().Sites {
		sites[s.Name] = s
	}
	has, ok := sites[timing.TmuxSite("has-session")]
	if !ok || has.Count != 2 {
		t.Errorf("tmux.has-session = %+v (present %v), want Count 2", has, ok)
	}
	if has.Max < 50*time.Millisecond {
		t.Errorf("tmux.has-session Max = %s, want at least the stub's 50ms sleep", has.Max)
	}
	if ls, ok := sites[timing.TmuxSite("list-sessions")]; !ok || ls.Count != 1 {
		t.Errorf("tmux.list-sessions = %+v (present %v), want Count 1", ls, ok)
	}
}

func TestDriverWithoutTimingStillRuns(t *testing.T) {
	stubTmux(t, "0")
	d, err := NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	if !d.HasSession("s") {
		t.Error("HasSession = false against a tmux that answers success")
	}
}
