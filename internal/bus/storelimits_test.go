package bus

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/config"
)

// With no limits set the jetstream block is what it has always been, so a
// cluster that sets neither renders a byte-identical conf.
func TestRenderConfWithNoLimitsIsUnchanged(t *testing.T) {
	t.Parallel()
	conf, err := RenderConf(baseSpec())
	if err != nil {
		t.Fatal(err)
	}
	want := "jetstream {\n  store_dir: \"/state/nats/store\"\n  domain: kinu\n}\n"
	if !strings.Contains(conf, want) {
		t.Fatalf("jetstream block changed with no limits set:\n%s", conf)
	}
	if strings.Contains(conf, "max_file_store") || strings.Contains(conf, "max_memory_store") {
		t.Fatalf("a limit rendered with none set:\n%s", conf)
	}
}

func TestRenderConfRendersTheStoreLimitsInsideTheJetstreamBlock(t *testing.T) {
	t.Parallel()
	s := baseSpec()
	s.MaxFileStore, s.MaxMemoryStore = 2<<30, 256<<20
	conf, err := RenderConf(s)
	if err != nil {
		t.Fatal(err)
	}
	want := "jetstream {\n  store_dir: \"/state/nats/store\"\n  domain: kinu\n  max_file_store: 2147483648\n  max_memory_store: 268435456\n}\n"
	if !strings.Contains(conf, want) {
		t.Fatalf("limits not rendered inside the jetstream block:\n%s", conf)
	}
	only := baseSpec()
	only.MaxFileStore = 1 << 20
	conf, err = RenderConf(only)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(conf, "max_file_store: 1048576\n") || strings.Contains(conf, "max_memory_store") {
		t.Fatalf("one limit set must render only that limit:\n%s", conf)
	}
}

// A real nats-server accepts the rendered conf and reports the cap it was
// given, so the numbers are in the unit the broker reads.
func TestRenderedStoreLimitsAreAcceptedByNatsServer(t *testing.T) {
	ns, err := exec.LookPath("nats-server")
	if err != nil {
		t.Skip("nats-server not on PATH")
	}
	s := baseSpec()
	s.StoreDir = filepath.Join(t.TempDir(), "nats")
	s.MaxFileStore, s.MaxMemoryStore = 2<<30, 256<<20
	conf, err := RenderConf(s)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := RenderAuth(s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFiles(s.StoreDir, conf, auth); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(ns, "-t", "-c", filepath.Join(s.StoreDir, ConfName)).CombinedOutput()
	if err != nil {
		t.Fatalf("nats-server -t rejected the conf: %v\n%s\n%s", err, out, conf)
	}
	if _, err := os.Stat(filepath.Join(s.StoreDir, ConfName)); err != nil {
		t.Fatal(err)
	}
}

// The cluster's resolved limits reach the rendered file through the Manager,
// not just through RenderConf.
func TestManagerRendersTheResolvedStoreLimits(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state", "nats")
	rb := config.ResolvedBus{Managed: true, Listen: "127.0.0.1:" + strconv.Itoa(freePort(t)), URL: "nats://x", StoreDir: dir, MaxFileStore: 1 << 30, MaxMemoryStore: 64 << 20}
	m, err := NewManager(dir, "t"+strconv.Itoa(freePort(t)), rb, &teams{{Name: "ops", Workspace: "aae-orc"}}, func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ConfName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "max_file_store: 1073741824\n") || !strings.Contains(string(b), "max_memory_store: 67108864\n") {
		t.Fatalf("the manager's conf lacks the limits:\n%s", b)
	}
}
