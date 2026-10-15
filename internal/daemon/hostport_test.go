package daemon

import (
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/arcavenae/marvel/internal/knownhosts"
)

func TestHostPortOf(t *testing.T) {
	for _, tc := range []struct {
		addr, want string
		wantErr    bool
	}{
		{"mrvl://10.0.0.5:6785", "10.0.0.5:6785", false},
		{"mrvl://op@10.0.0.5:7000", "10.0.0.5:7000", false},
		{"mrvl://10.0.0.5", "10.0.0.5:" + DefaultMRVLPort, false},
		{"mrvl://op@kinu", "kinu:" + DefaultMRVLPort, false},
		{"ssh://op@10.0.0.5:6785", "10.0.0.5:6785", false},
		{"ssh://op@10.0.0.5/run/marvel.sock", "", true},
		{"ssh://op@10.0.0.5:22/run/marvel.sock", "", true},
		{"ssh://op@10.0.0.5", "", true},
		{"/run/marvel.sock", "", true},
		{"tcp://10.0.0.5:6785", "", true},
		{"", "", true},
	} {
		got, err := HostPortOf(tc.addr)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("HostPortOf(%q) = %q, %v; want %q, error=%v", tc.addr, got, err, tc.want, tc.wantErr)
		}
		if err != nil && !strings.Contains(err.Error(), "address") {
			t.Errorf("HostPortOf(%q) error %q should name the address problem", tc.addr, err)
		}
	}
}

// The daemon's own SSH server presents the key HostKeyFingerprint reports, and
// the probe must read that one, with no credentials and without a daemon.
func TestProbeReadsTheKeyTheDaemonSSHServerPresents(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv, err := newSSHServer(nil)
	if err != nil {
		t.Fatalf("newSSHServer: %v", err)
	}
	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(srv.Stop)

	got, err := knownhosts.Probe(srv.Addr().String())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	want, err := HostKeyFingerprint()
	if err != nil {
		t.Fatalf("HostKeyFingerprint: %v", err)
	}
	if fp := ssh.FingerprintSHA256(got); fp != want {
		t.Fatalf("probe read %s, the daemon reports %s", fp, want)
	}
}
