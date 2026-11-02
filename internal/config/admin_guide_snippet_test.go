package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The admin guide's "Declaring it" snippet is what a reader pastes into
// config.yaml to get a managed broker. It must load, and it must declare a
// managed bus: a managed bus with no listen address, or a services list that is
// not under a cluster, is refused at read time or silently ignored.
// Not parallel: it sets MARVEL_HOME.
func TestAdminGuideDeclaringItSnippetLoadsAsAManagedBus(t *testing.T) {
	raw, err := os.ReadFile("../../docs/admin-guide.md")
	if err != nil {
		t.Fatal(err)
	}
	guide := string(raw)
	start := strings.Index(guide, "### Declaring it")
	if start < 0 {
		t.Fatal("the admin guide has no \"Declaring it\" heading")
	}
	m := regexp.MustCompile("(?s)```yaml\n(.*?)```").FindStringSubmatch(guide[start:])
	if m == nil {
		t.Fatal("no yaml block after \"Declaring it\"")
	}

	// A short root: the socket path under it must fit a unix socket's limit.
	home, err := os.MkdirTemp("/tmp", "ag")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("MARVEL_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(m[1]), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("the snippet does not load: %v", err)
	}
	if len(cfg.Clusters) == 0 {
		t.Fatal("the snippet declares no cluster, so its bus entry is never read")
	}
	c := &cfg.Clusters[0]
	if err := ValidateServices(c); err != nil {
		t.Fatalf("the bus entry is refused: %v", err)
	}
	svc, err := c.BusService()
	if err != nil || svc == nil {
		t.Fatalf("the snippet declares no bus: %+v, %v", svc, err)
	}
	if svc.Mode != "managed" {
		t.Errorf("the snippet declares mode %q, want managed", svc.Mode)
	}
	b, err := svc.BusSpec()
	if err != nil || b.Listen == "" {
		t.Errorf("a managed bus needs an explicit listen address: %+v, %v", b, err)
	}
}
