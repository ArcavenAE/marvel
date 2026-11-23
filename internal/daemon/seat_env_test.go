package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/tmux"
)

// loadSeatEnv reads seat_env from the cluster entry whose socket is ours and the
// driver then starts panes with it. A tmux that only logs its arguments stands in
// for the real one. A seat_env that does not check out is ignored, not fatal.
func TestLoadSeatEnvReachesTheDriversPanes(t *testing.T) {
	const sock = "/tmp/m-seatenv.sock"
	for _, tc := range []struct {
		name, yaml string
		wantPath   bool
		wantUnset  bool
	}{
		{"matching cluster", "clusters:\n  - name: mine\n    socket: " + sock + "\n    seat_env:\n      path_prepend: [/opt/x/bin]\n      unset: [AWS_PROFILE]\n", true, true},
		{"another cluster's block is not used", "clusters:\n  - name: other\n    socket: /tmp/m-other.sock\n    seat_env:\n      unset: [AWS_PROFILE]\n  - name: mine\n    socket: " + sock + "\n", false, false},
		{"a bad block is ignored whole", "clusters:\n  - name: mine\n    socket: " + sock + "\n    seat_env:\n      path_prepend: [/opt/x/bin]\n      unset: [\"bad name\"]\n", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			if err := os.MkdirAll(filepath.Join(home, ".marvel"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, ".marvel", "config.yaml"), []byte(tc.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			log := filepath.Join(bin, "calls")
			if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\necho \"$@\" >> '"+log+"'\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+":/usr/bin:/bin")
			drv, err := tmux.NewDriver()
			if err != nil {
				t.Fatal(err)
			}
			d := &Daemon{driver: drv}
			d.loadSeatEnv(sock)
			if _, err := drv.NewPaneAt("sess", "claude", "t", "", nil, false); err != nil {
				t.Fatalf("NewPaneAt: %v", err)
			}
			b, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			got := string(b)
			if has := strings.Contains(got, "-e PATH=/opt/x/bin:"); has != tc.wantPath {
				t.Errorf("PATH prepend present = %v, want %v: %s", has, tc.wantPath, got)
			}
			if has := strings.Contains(got, " -u AWS_PROFILE "); has != tc.wantUnset {
				t.Errorf("unset AWS_PROFILE present = %v, want %v: %s", has, tc.wantUnset, got)
			}
		})
	}
}
