package knownhosts

import (
	"crypto/ed25519"
	"crypto/rand"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/arcavenae/marvel/internal/paths"
)

// hostKeyServer serves an SSH handshake on loopback with a fresh ed25519 host
// key and returns its address and public key. Every connection is dropped
// once the handshake ends.
func hostKeyServer(t *testing.T) (string, ssh.PublicKey) {
	t.Helper()
	addr, key, _ := hostKeyServerCountingAuth(t)
	return addr, key
}

// hostKeyServerCountingAuth is hostKeyServer plus a count of the
// authentication attempts the server saw, so a test can show a client stopped
// at the host key.
func hostKeyServerCountingAuth(t *testing.T) (string, ssh.PublicKey, *atomic.Int32) {
	t.Helper()
	var attempts atomic.Int32
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		NoClientAuth:    true,
		AuthLogCallback: func(ssh.ConnMetadata, string, error) { attempts.Add(1) },
	}
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
	return ln.Addr().String(), signer.PublicKey(), &attempts
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

// snapshotMarvelHome returns every file under the test HOME's .marvel
// directory with its contents, so a test can show a probe wrote nothing.
func snapshotMarvelHome(t *testing.T, home string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	root := filepath.Join(home, ".marvel")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			snap[rel+"/"] = ""
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snap[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// The marvel home exists and holds a known_hosts entry before each probe, so a
// write by the probe would land and show; a home that does not exist would let
// a planted write fail silently.
func TestProbeRecordsNothingOnAnyPath(t *testing.T) {
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedAddr := closed.Addr().String()
	_ = closed.Close()

	hangsUp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hangsUp.Close() })
	go func() {
		for {
			c, err := hangsUp.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	okAddr, _ := hostKeyServer(t)

	for _, tc := range []struct {
		name    string
		addr    string
		wantErr bool
	}{
		{"a peer that presents a key", okAddr, false},
		{"a dial error", closedAddr, true},
		{"a handshake error", hangsUp.Addr().String(), true},
	} {
		home := t.TempDir()
		t.Setenv("HOME", home)
		layout, err := paths.Default()
		if err != nil {
			t.Fatal(err)
		}
		if err := layout.EnsureHome(); err != nil {
			t.Fatal(err)
		}
		other := testKey(t)
		if err := Trust(layout, "10.9.9.9:6785", other); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".marvel", "config.yaml"), []byte("clusters: []\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		before := snapshotMarvelHome(t, home)

		_, err = Probe(tc.addr)

		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
		if after := snapshotMarvelHome(t, home); !reflect.DeepEqual(before, after) {
			t.Errorf("%s: the probe changed the marvel home:\nbefore %q\nafter  %q", tc.name, before, after)
		}
	}
}

func TestProbeStopsAtTheHostKeyBeforeAuthentication(t *testing.T) {
	addr, _, attempts := hostKeyServerCountingAuth(t)

	if _, err := Probe(addr); err != nil {
		t.Fatalf("Probe: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	if n := attempts.Load(); n != 0 {
		t.Fatalf("the probe went on to authentication: the server saw %d attempts", n)
	}
}

func testKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return k
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
