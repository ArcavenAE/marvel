package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/arcavenae/marvel/internal/bus"
)

// The reload after a render goes through the lifecycle the daemon holds. The
// test drives attachServices, the path production takes, with a managed broker,
// and checks that the manager's reloader is the daemon's lifecycle and not the
// concrete supervisor behind it.
func TestAttachServicesReloadsThroughTheLifecycle(t *testing.T) {
	if _, err := exec.LookPath("nats-server"); err != nil {
		t.Skip("nats-server not on PATH")
	}
	_, port := freeBrokerPort(t)

	d := newHandlerDaemon(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MARVEL_SOCKET", "")
	sock := filepath.Join(home, ".marvel", "run", "marvel.sock")
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "clusters:\n  - name: c1\n    socket: " + sock + "\n    bus:\n      managed: true\n      listen: 127.0.0.1:" + strconv.Itoa(port) + "\ncurrent_cluster: c1\n"
	if err := os.WriteFile(filepath.Join(home, ".marvel", "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.attachServices(sock); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.stopBus(true) })
	if d.bus == nil || d.lifecycle == nil {
		t.Fatalf("setup: managed broker did not attach: bus=%v lifecycle=%v", d.bus, d.lifecycle)
	}
	if got := d.bus.Reloader; got != bus.Reloader(d.lifecycle) {
		t.Errorf("the manager reloads through %T, want the daemon's lifecycle %T", got, d.lifecycle)
	}
}
