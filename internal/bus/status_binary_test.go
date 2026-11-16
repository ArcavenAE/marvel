package bus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// bus status names the broker binary (aae-orc-ktts0): the path the supervisor
// execs, where that path really points, whether it is still there, and the
// version the running broker reports on /varz, so a binary removed by a
// cleanup, or a version that moved under a running broker, shows by name.
//
// The tests read the Status through its JSON, the form `marvel bus status`
// moves over the daemon socket, so they compile against the Status of today
// and fail on what it does not yet carry.

// statusFields is the Status as a flat JSON object.
func statusFields(t *testing.T, s *Supervisor) map[string]any {
	t.Helper()
	raw, err := json.Marshal(s.Status())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestBusStatusReportsConfiguredRealpathAndVersion(t *testing.T) {
	root := t.TempDir()
	real := fakeNATS(t, filepath.Join(root, "keg", "2.14.6", "bin"), "v2.14.6")
	if err := os.MkdirAll(filepath.Join(root, "opt", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "opt", "bin", "nats-server")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	wantReal, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}

	s, err := NewSupervisor(binaryManager(t, link), t.TempDir(), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	attach(t, s, newProcTable(t))

	// A configured symlink: the path, its realpath, present, and the version
	// the binary on disk reports.
	got := statusFields(t, s)
	if got["binary"] != link {
		t.Errorf("binary = %v, want the configured path %q", got["binary"], link)
	}
	if got["binary_configured"] != true {
		t.Errorf("binary_configured = %v, want true for a bus.binary path", got["binary_configured"])
	}
	if got["binary_realpath"] != wantReal {
		t.Errorf("binary_realpath = %v, want %q", got["binary_realpath"], wantReal)
	}
	if got["binary_exists"] != true {
		t.Errorf("binary_exists = %v, want true while the file is there", got["binary_exists"])
	}
	if got["version"] != "v2.14.6" {
		t.Errorf("version = %v, want the binary on disk's v2.14.6", got["version"])
	}

	// The running broker's own report, read from /varz by the structural
	// reading, is a separate field, so a broker that outlives a binary swap
	// shows both.
	if err := json.Unmarshal([]byte(`{"authorized":true,"version":"v2.14.5"}`), &s.structure); err != nil {
		t.Fatal(err)
	}
	s.structureKnown = true
	got = statusFields(t, s)
	if got["serving_version"] != "v2.14.5" {
		t.Errorf("serving_version = %v, want the /varz version v2.14.5", got["serving_version"])
	}

	// A keg removed under the running broker: the path is still configured,
	// it points nowhere, and the status says so by name.
	if err := os.RemoveAll(filepath.Join(root, "keg")); err != nil {
		t.Fatal(err)
	}
	got = statusFields(t, s)
	if got["binary"] != link {
		t.Errorf("after the keg is gone: binary = %v, want %q kept", got["binary"], link)
	}
	if got["binary_exists"] != false {
		t.Errorf("after the keg is gone: binary_exists = %v, want false", got["binary_exists"])
	}
	if v, ok := got["binary_realpath"]; ok && v != "" {
		t.Errorf("after the keg is gone: binary_realpath = %v, want none", v)
	}
}

// With bus.binary unset the broker is the PATH lookup, and the status says
// so: the path is the one found, and it is not marked configured.
func TestBusStatusBinaryFromPathIsNotConfigured(t *testing.T) {
	root := t.TempDir()
	onPath := fakeNATS(t, filepath.Join(root, "pathdir"), "v1.0.0-path")
	t.Setenv("PATH", filepath.Dir(onPath)+string(os.PathListSeparator)+"/usr/bin:/bin")

	s, err := NewSupervisor(binaryManager(t, ""), t.TempDir(), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	attach(t, s, newProcTable(t))
	got := statusFields(t, s)
	if got["binary"] != onPath {
		t.Errorf("binary = %v, want the PATH lookup %q", got["binary"], onPath)
	}
	if got["binary_configured"] == true {
		t.Errorf("binary_configured = true for a PATH lookup")
	}
	if got["binary_exists"] != true {
		t.Errorf("binary_exists = %v, want true", got["binary_exists"])
	}
}
