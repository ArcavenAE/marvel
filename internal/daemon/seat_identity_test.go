package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

// The daemon hands the session manager its own cluster entry name, and nothing
// else: an adopted broker with no hub section still gets a seat URI.
func TestAttachBusSetsTheSeatCluster(t *testing.T) {
	d := newHandlerDaemon(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MARVEL_SOCKET", "")
	sock := filepath.Join(home, ".marvel", "run", "marvel.sock")
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "clusters:\n  - name: kinu\n    socket: " + sock + "\n    bus:\n      managed: false\n      url: nats://127.0.0.1:4222\ncurrent_cluster: kinu\n"
	if err := os.WriteFile(filepath.Join(home, ".marvel", "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.attachServices(sock); err != nil {
		t.Fatal(err)
	}
	if d.sessMgr.SeatCluster != "kinu" {
		t.Errorf("SeatCluster = %q, want kinu", d.sessMgr.SeatCluster)
	}
}
