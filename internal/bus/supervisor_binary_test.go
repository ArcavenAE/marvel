package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/config"
)

// bus.binary (aae-orc-n5a4y): the supervisor execs the configured absolute
// nats-server instead of resolving one on PATH.

// fakeNATS writes an executable that answers --version like nats-server does,
// with the given version token, and returns its absolute path.
func fakeNATS(t *testing.T, dir, version string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "nats-server")
	body := "#!/bin/sh\necho \"nats-server: " + version + "\"\n"
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// binaryManager is a managed-bus Manager that carries binary in its resolved
// config, the way the daemon builds it from the cluster's bus: block.
func binaryManager(t *testing.T, binary string) *Manager {
	t.Helper()
	rb := config.ResolvedBus{Managed: true, Listen: "127.0.0.1:1", StoreDir: t.TempDir(), Binary: binary}
	m, err := NewManager(t.TempDir(), "c1", rb, &teams{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// With bus.binary set, the supervisor takes that file as the broker and
// reports its version, even when PATH offers a different nats-server first.
func TestSupervisorExecsConfiguredBinaryNotPath(t *testing.T) {
	root := t.TempDir()
	onPath := fakeNATS(t, filepath.Join(root, "pathdir"), "v1.0.0-path")
	configured := fakeNATS(t, filepath.Join(root, "pinned", "bin"), "v9.9.9-configured")
	t.Setenv("PATH", filepath.Dir(onPath)+string(os.PathListSeparator)+"/usr/bin:/bin")

	s, err := NewSupervisor(binaryManager(t, configured), t.TempDir(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewSupervisor with a configured binary: %v", err)
	}
	attach(t, s, newProcTable(t))
	if s.binary != configured {
		t.Errorf("supervisor binary = %q, want the configured %q (PATH offered %q)", s.binary, configured, onPath)
	}
	if s.version != "v9.9.9-configured" {
		t.Errorf("supervisor version = %q, want the configured binary's version", s.version)
	}
}

// With the key unset, today's lookup is unchanged: the first nats-server on
// PATH is the broker.
func TestSupervisorWithoutConfiguredBinaryUsesPath(t *testing.T) {
	root := t.TempDir()
	onPath := fakeNATS(t, filepath.Join(root, "pathdir"), "v1.0.0-path")
	t.Setenv("PATH", filepath.Dir(onPath)+string(os.PathListSeparator)+"/usr/bin:/bin")

	s, err := NewSupervisor(binaryManager(t, ""), t.TempDir(), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	attach(t, s, newProcTable(t))
	if s.binary != onPath || s.version != "v1.0.0-path" {
		t.Errorf("unset key: binary %q version %q, want the PATH lookup %q v1.0.0-path", s.binary, s.version, onPath)
	}
}

// A configured path that is not there is an error naming the path and the
// key. It never falls back to PATH: a silent fallback would exec a binary the
// operator did not choose, with the leaf seed in its environment.
func TestSupervisorRefusesAMissingConfiguredBinary(t *testing.T) {
	root := t.TempDir()
	onPath := fakeNATS(t, filepath.Join(root, "pathdir"), "v1.0.0-path")
	t.Setenv("PATH", filepath.Dir(onPath)+string(os.PathListSeparator)+"/usr/bin:/bin")
	missing := filepath.Join(root, "gone", "nats-server")

	_, err := NewSupervisor(binaryManager(t, missing), t.TempDir(), t.TempDir(), nil)
	if err == nil {
		t.Fatal("NewSupervisor accepted a configured binary that does not exist and fell back to PATH")
	}
	if !strings.Contains(err.Error(), missing) || !strings.Contains(err.Error(), "bus.binary") {
		t.Errorf("error %q does not name the key and the path", err)
	}
}

// A directory, and a file that is not executable, are not a broker.
func TestSupervisorRefusesAConfiguredBinaryThatCannotRun(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "adir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(root, "plain")
	if err := os.WriteFile(plain, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, bin := range map[string]string{"directory": dir, "not executable": plain} {
		_, err := NewSupervisor(binaryManager(t, bin), t.TempDir(), t.TempDir(), nil)
		if err == nil {
			t.Errorf("%s: NewSupervisor accepted %q", name, bin)
			continue
		}
		if !strings.Contains(err.Error(), bin) {
			t.Errorf("%s: error %q does not name the path", name, err)
		}
	}
}

// The supervisor checks the path itself, so a relative path or a shim
// directory that reached it without the config validator is still refused
// before anything is exec'd.
func TestSupervisorRefusesARelativeOrShimConfiguredBinary(t *testing.T) {
	root := t.TempDir()
	shim := fakeNATS(t, filepath.Join(root, "mise", "shims"), "v1.0.0-shim")
	t.Chdir(root)
	if err := os.MkdirAll("bin", 0o755); err != nil {
		t.Fatal(err)
	}
	fakeNATS(t, "bin", "v1.0.0-rel")
	for name, bin := range map[string]string{
		"relative":          "bin/nats-server",
		"dot-relative":      "./bin/nats-server",
		"bare name":         "nats-server",
		"shim directory":    shim,
		"~-relative":        "~/bin/nats-server",
		"relative via dots": "../" + filepath.Base(root) + "/bin/nats-server",
	} {
		_, err := NewSupervisor(binaryManager(t, bin), t.TempDir(), t.TempDir(), nil)
		if err == nil {
			t.Errorf("%s: NewSupervisor accepted %q", name, bin)
			continue
		}
		if !strings.Contains(err.Error(), "bus.binary") {
			t.Errorf("%s: error %q does not name the key", name, err)
		}
	}
}
