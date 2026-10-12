package knownhosts

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
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

func TestProbeReturnsTheKeyThePeerPresents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	addr, want := hostKeyServer(t)

	got, err := Probe(addr)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if string(got.Marshal()) != string(want.Marshal()) {
		t.Fatalf("probe read %s, server key is %s", ssh.FingerprintSHA256(got), ssh.FingerprintSHA256(want))
	}
}

func TestProbeRecordsNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	addr, _ := hostKeyServer(t)

	if _, err := Probe(addr); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".marvel", "known_hosts")); err == nil {
		data, _ := os.ReadFile(filepath.Join(home, ".marvel", "known_hosts"))
		if len(data) != 0 {
			t.Fatalf("a probe wrote to known_hosts:\n%s", data)
		}
	}
}

func TestProbeFailsWhenNothingListens(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	if _, err := Probe(addr); err == nil {
		t.Fatal("Probe to a closed port returned no error")
	}
}
