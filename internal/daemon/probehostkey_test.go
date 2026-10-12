package daemon

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// hostKeyServer serves an SSH handshake on loopback with a fresh ed25519 host
// key and returns its address and public key. Every connection is dropped
// once the handshake ends.
func hostKeyServer(t *testing.T) (string, ssh.PublicKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				if sc, _, _, err := ssh.NewServerConn(c, cfg); err == nil {
					_ = sc.Close()
				}
			}()
		}
	}()
	return ln.Addr().String(), signer.PublicKey()
}

func TestProbeHostKeyReturnsTheKeyThePeerPresents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	addr, want := hostKeyServer(t)

	got, err := ProbeHostKey("mrvl://op@" + addr)
	if err != nil {
		t.Fatalf("ProbeHostKey: %v", err)
	}
	if string(got.Marshal()) != string(want.Marshal()) {
		t.Fatalf("probe read %s, server key is %s", ssh.FingerprintSHA256(got), ssh.FingerprintSHA256(want))
	}
}

func TestProbeHostKeyRecordsNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	addr, _ := hostKeyServer(t)

	if _, err := ProbeHostKey("mrvl://" + addr); err != nil {
		t.Fatalf("ProbeHostKey: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".marvel", "known_hosts")); err == nil {
		data, _ := os.ReadFile(filepath.Join(home, ".marvel", "known_hosts"))
		if len(data) != 0 {
			t.Fatalf("a probe wrote to known_hosts:\n%s", data)
		}
	}
}

func TestProbeHostKeyFailsWhenNothingListens(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	if _, err := ProbeHostKey("mrvl://" + addr); err == nil {
		t.Fatal("ProbeHostKey to a closed port returned no error")
	}
}

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
func TestProbeHostKeyReadsTheKeyTheDaemonSSHServerPresents(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv, err := newSSHServer(nil)
	if err != nil {
		t.Fatalf("newSSHServer: %v", err)
	}
	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(srv.Stop)

	got, err := ProbeHostKey("mrvl://" + srv.Addr().String())
	if err != nil {
		t.Fatalf("ProbeHostKey: %v", err)
	}
	want, err := HostKeyFingerprint()
	if err != nil {
		t.Fatalf("HostKeyFingerprint: %v", err)
	}
	if fp := ssh.FingerprintSHA256(got); fp != want {
		t.Fatalf("probe read %s, the daemon reports %s", fp, want)
	}
}
