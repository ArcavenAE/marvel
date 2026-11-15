package daemon

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeServicesConfig puts a client config in a scratch HOME and returns the
// socket the cluster entry names.
func writeServicesConfig(t *testing.T, services string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MARVEL_SOCKET", "")
	sock := filepath.Join(home, ".marvel", "run", "marvel.sock")
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "clusters:\n  - name: kinu\n    socket: " + sock + "\n    services:\n" + services + "current_cluster: kinu\n"
	if err := os.WriteFile(filepath.Join(home, ".marvel", "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return sock
}

const adoptedBusEntry = "      - name: bus\n        class: message-bus\n        provider: nats-server\n        mode: adopted\n        url: nats://127.0.0.1:4222\n"

// A cluster that declares a class marvel does not carry yet keeps its bus.
// marvel#916, docs/design/services-shape-requirements.md MS-21.
func TestAttachServicesKeepsBusBesideUnregisteredClass(t *testing.T) {
	sock := writeServicesConfig(t, adoptedBusEntry+"      - name: llm\n        class: inference-gateway\n        provider: litellm\n        mode: adopted\n        url: http://127.0.0.1:4000\n")
	d := newHandlerDaemon(t)
	if err := d.attachServices(sock); err != nil {
		t.Fatalf("attachServices: %v", err)
	}
	if d.sessMgr.Bus == nil || d.sessMgr.Bus.URL() != "nats://127.0.0.1:4222" {
		t.Fatalf("the bus was dropped beside an unregistered class: Bus=%v", d.sessMgr.Bus)
	}
}

func TestAttachServicesKeepsBusBesideUnregisteredProvider(t *testing.T) {
	sock := writeServicesConfig(t, "      - name: cache\n        class: message-bus\n        provider: redis\n        mode: adopted\n        url: redis://127.0.0.1:6379\n"+adoptedBusEntry)
	d := newHandlerDaemon(t)
	if err := d.attachServices(sock); err != nil {
		t.Fatalf("attachServices: %v", err)
	}
	if d.sessMgr.Bus == nil || d.sessMgr.Bus.URL() != "nats://127.0.0.1:4222" {
		t.Fatalf("the bus was dropped beside an unregistered provider: Bus=%v", d.sessMgr.Bus)
	}
}

func TestAttachServicesLogsEachSkippedEntryByName(t *testing.T) {
	sock := writeServicesConfig(t, adoptedBusEntry+"      - name: llm\n        class: inference-gateway\n        provider: litellm\n        mode: adopted\n        url: http://127.0.0.1:4000\n")
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	d := newHandlerDaemon(t)
	if err := d.attachServices(sock); err != nil {
		t.Fatalf("attachServices: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"llm"`) || !strings.Contains(out, "skipped") {
		t.Errorf("no log line names the skipped entry %q:\n%s", "llm", out)
	}
	if strings.Contains(out, "nothing attached") {
		t.Errorf("log still says nothing attached:\n%s", out)
	}
}

// Characterization: any other refusal still refuses the whole list, and start
// is not refused.
func TestAttachServicesDuplicateNameStillRefusesWholeList(t *testing.T) {
	sock := writeServicesConfig(t, adoptedBusEntry+adoptedBusEntry)
	d := newHandlerDaemon(t)
	if err := d.attachServices(sock); err != nil {
		t.Fatalf("a refused list failed start: %v", err)
	}
	if d.sessMgr.Bus != nil || d.bus != nil {
		t.Error("a list with a duplicate name attached a bus")
	}
}
