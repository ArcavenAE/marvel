package bus

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/config"
)

type failingReloader struct{}

func (failingReloader) Reload() error { return errors.New("reload refused") }

// TestSetLeafAttachedRollsBackOnRegenerateFailure proves a failed apply does
// not strand the persisted decision ahead of the broker: when the reload
// fails, the in-memory flag and the on-disk state return to their previous
// value, so a retry re-applies instead of the prev==attached guard turning it
// into a silent no-op (codex review P1-3).
func TestSetLeafAttachedRollsBackOnRegenerateFailure(t *testing.T) {
	dir := t.TempDir()
	rb := config.ResolvedBus{Managed: true, Listen: "127.0.0.1:4222", StoreDir: dir, HubURL: "nats-leaf://127.0.0.1:1"}
	m, err := NewManager(dir, "kinu", rb, &teams{}, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	// Render the initial attached conf so a later toggle is a real change.
	if _, err := m.Render(); err != nil {
		t.Fatal(err)
	}
	if !m.LeafAttached() {
		t.Fatal("default should be attached")
	}
	m.Reloader = failingReloader{}

	// Disconnect: the leaf block changes, the reload fails, the op rolls back.
	if _, err := m.SetLeafAttached(false); err == nil {
		t.Fatal("expected the reload failure to surface")
	}
	if !m.LeafAttached() {
		t.Error("in-memory decision not rolled back to attached after failure")
	}
	if !readLeafAttached(filepath.Join(dir, LeafStateName)) {
		t.Error("persisted decision not rolled back to attached after failure")
	}

	// A retry with a broker that accepts the reload must re-apply, not no-op.
	r := &countingReloader{}
	m.Reloader = r
	if _, err := m.SetLeafAttached(false); err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
	if r.n == 0 {
		t.Error("retry after rollback was a silent no-op; the broker was never reloaded")
	}
	if m.LeafAttached() {
		t.Error("after a successful disconnect the leaf should be detached")
	}
	if readLeafAttached(filepath.Join(dir, LeafStateName)) {
		t.Error("successful disconnect not persisted as detached")
	}
}

func TestSeedFingerprintDistinguishesSeeds(t *testing.T) {
	a := SeedFingerprint([]byte("seed-A"))
	if a != SeedFingerprint([]byte("seed-A")) {
		t.Error("fingerprint is not stable for the same seed")
	}
	if a == SeedFingerprint([]byte("seed-B")) {
		t.Error("different seeds share a fingerprint")
	}
	if a == "" {
		t.Error("fingerprint is empty")
	}
}

// TestManagerRenderCarriesTheHubCAFileToTheLeafRemote covers the link between
// config and render: bus.hub.ca_file resolves into ResolvedBus.HubCAFile, and
// Manager.Render must hand it to the renderer, or a managed broker cannot join
// a TLS hub (marvel#278). The config tests stop at the resolved value and the
// render tests start from a hand-built Spec, so this is the one place the copy
// between them is exercised. It is a coverage test, not red-first: the code it
// covers shipped in #285.
func TestManagerRenderCarriesTheHubCAFileToTheLeafRemote(t *testing.T) {
	dir := t.TempDir()
	const ca = "/etc/marvel/hub-ca.pem"
	rb := config.ResolvedBus{
		Managed:   true,
		Listen:    "127.0.0.1:4222",
		StoreDir:  dir,
		HubURL:    "tls://hub.example:7442",
		HubCAFile: ca,
	}
	m, err := NewManager(dir, "kinu", rb, &teams{}, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Render(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ConfName))
	if err != nil {
		t.Fatal(err)
	}
	conf := string(raw)
	want := `{ urls: ["tls://hub.example:7442"], nkey: $DIRECTOR_LEAF_NKEY, tls { ca_file: "` + ca + `" } }`
	if !strings.Contains(conf, want) {
		t.Errorf("the rendered leaf remote lacks the hub CA:\n%s", conf)
	}
	if n := strings.Count(conf, "ca_file"); n != 1 {
		t.Errorf("ca_file appears %d times, want once, in the leaf remote:\n%s", n, conf)
	}

	// Control: with no CA configured the same render carries no tls block, so
	// the assertion above is about the field and not about the hub URL.
	rb.HubCAFile = ""
	dir2 := t.TempDir()
	rb.StoreDir = dir2
	m2, err := NewManager(dir2, "kinu", rb, &teams{}, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m2.Render(); err != nil {
		t.Fatal(err)
	}
	raw2, err := os.ReadFile(filepath.Join(dir2, ConfName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw2), "ca_file") {
		t.Errorf("a render with no hub CA still carries ca_file:\n%s", raw2)
	}
}
