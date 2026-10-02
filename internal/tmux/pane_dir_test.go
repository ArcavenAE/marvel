package tmux

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A pane started with a directory runs there, whatever the tmux server's own
// directory is (docs/design/session-working-directory.md, decision 4).
func TestNewPaneAtStartsInTheDirectory(t *testing.T) {
	skipIfNoTmux(t)
	socket := fmt.Sprintf("marvel-test-panedir-%d", os.Getpid())
	t.Setenv("MARVEL_TMUX_SOCKET", socket)
	t.Cleanup(func() { killTestServer(socket) })

	d, err := NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	if err := d.NewSession("panedir"); err != nil {
		t.Fatalf("new session: %v", err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "cwd.txt")
	if _, err := d.NewPaneAt("panedir", "pwd -P > "+out+".tmp && mv "+out+".tmp "+out, "", dir, nil, false); err != nil {
		t.Fatalf("new pane: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, err := os.ReadFile(out)
		if err == nil {
			if got := strings.TrimSpace(string(b)); got != dir {
				t.Fatalf("pane cwd = %q, want %q", got, dir)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane never reported its directory: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
