package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/tmux"
)

// A cluster that maps a seat secret keeps the variable out of the tmux server
// and every pane, and strips it from a running server by name. A cluster that
// declares none changes nothing. The tmux here logs its arguments and the names
// of the variables it was started with, never a value.
func TestLoadSeatEnvKeepsADeclaredSecretOutOfTheServerAndPanes(t *testing.T) {
	const sock = "/tmp/m-seatsecret.sock"
	for _, tc := range []struct {
		name, yaml string
		want       bool
	}{
		{"declared", "clusters:\n  - name: mine\n    socket: " + sock + "\n    seat_env:\n      secrets:\n        bd: BEADS_DOLT_PASSWORD\n", true},
		{"not declared", "clusters:\n  - name: mine\n    socket: " + sock + "\n", false},
		{"a bad mapping is ignored whole", "clusters:\n  - name: mine\n    socket: " + sock + "\n    seat_env:\n      secrets:\n        bd: \"not a name\"\n", false},
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
			script := "#!/bin/sh\necho \"ARGS $@\" >> '" + log + "'\nenv | cut -d= -f1 | sed 's/^/ENV /' >> '" + log + "'\n"
			if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+":/usr/bin:/bin")
			t.Setenv("BEADS_DOLT_PASSWORD", "mxfake-0042")
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
			if strings.Contains(got, "mxfake-0042") {
				t.Error("the value reached the tmux log")
			}
			strippedByName := strings.Contains(got, "set-environment -g -u BEADS_DOLT_PASSWORD")
			clientClean := !strings.Contains(got, "ENV BEADS_DOLT_PASSWORD\n")
			paneUnset := strings.Contains(got, " -u BEADS_DOLT_PASSWORD ")
			if strippedByName != tc.want || clientClean != tc.want || paneUnset != tc.want {
				t.Errorf("declared=%v: stripped by name %v, client clean %v, pane unsets %v\n%s", tc.want, strippedByName, clientClean, paneUnset, got)
			}
		})
	}
}
