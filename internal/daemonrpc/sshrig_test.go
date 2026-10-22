package daemonrpc

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/arcavenae/marvel/internal/knownhosts"
	"github.com/arcavenae/marvel/internal/paths"
)

// sshRig is the S rig: a minimal SSH server on 127.0.0.1:0, built from
// x/crypto alone, that accepts one client key and answers one JSON-RPC
// request on a session channel. It imports no daemon code.
type sshRig struct {
	addr    string        // mrvl:// address of the listener
	hostKey ssh.PublicKey // the key the server presents
	log     *rigLog       // what clients did to the listener
}

// rigLog records what reached the listener: the connections accepted and the
// SHA256 fingerprint of each client key offered, in order.
type rigLog struct {
	mu    sync.Mutex
	conns int
	keys  []string
}

func (l *rigLog) conn() { l.mu.Lock(); l.conns++; l.mu.Unlock() }

func (l *rigLog) key(k ssh.PublicKey) {
	l.mu.Lock()
	l.keys = append(l.keys, ssh.FingerprintSHA256(k))
	l.mu.Unlock()
}

// seen returns the connections accepted and the key fingerprints offered.
func (l *rigLog) seen() (int, []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.conns, append([]string(nil), l.keys...)
}

// newSigner returns a fresh ed25519 signer and its private key in OpenSSH
// PEM form, for writing to an identity file.
func newSigner(t *testing.T) (ssh.Signer, []byte) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	return signer, pem.EncodeToMemory(block)
}

// writeIdentity writes a private key to a 0600 file in a temp directory.
func writeIdentity(t *testing.T, pemBytes []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "id")
	if err := os.WriteFile(p, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// startSSHRig starts the server, accepting only allowed as a client key.
func startSSHRig(t *testing.T, allowed ssh.PublicKey) sshRig {
	t.Helper()
	return startSSHRigWith(t, allowed, rigAnswers)
}

// rigMode picks where the rig stops answering.
type rigMode int

const (
	rigAnswers     rigMode = iota // answers the request
	rigSilentReply                // reads the request and never answers
	rigStallShell                 // never replies to the shell request
	rigStallAuth                  // never decides on the offered key
)

// startSSHRigWith is startSSHRig that stops answering where mode says,
// until the client closes or the test ends.
func startSSHRigWith(t *testing.T, allowed ssh.PublicKey, mode rigMode) sshRig {
	t.Helper()
	stall := make(chan struct{})
	t.Cleanup(func() { close(stall) })
	hostSigner, _ := newSigner(t)
	rlog := &rigLog{}
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			rlog.key(key)
			if mode == rigStallAuth {
				<-stall
			}
			if string(key.Marshal()) == string(allowed.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("key not accepted")
		},
	}
	cfg.AddHostKey(hostSigner)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			rlog.conn()
			go serveSSHRig(c, cfg, mode)
		}
	}()
	return sshRig{addr: "mrvl://rig@" + ln.Addr().String(), hostKey: hostSigner.PublicKey(), log: rlog}
}

func serveSSHRig(c net.Conn, cfg *ssh.ServerConfig, mode rigMode) {
	defer func() { _ = c.Close() }()
	sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	defer func() { _ = sc.Close() }()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "session only")
			continue
		}
		ch, in, err := nc.Accept()
		if err != nil {
			return
		}
		go func() {
			for r := range in {
				if r.Type == "shell" && mode == rigStallShell {
					continue
				}
				_ = r.Reply(r.Type == "shell", nil)
			}
		}()
		var req Request
		if mode == rigSilentReply || mode == rigStallShell {
			_ = json.NewDecoder(ch).Decode(&req)
			_, _ = io.Copy(io.Discard, ch)
		} else if err := json.NewDecoder(ch).Decode(&req); err == nil {
			_ = json.NewEncoder(ch).Encode(Response{Result: json.RawMessage(`"ok"`)})
		}
		_ = ch.Close()
	}
}

// trustRigHost records the rig's host key in the scratch home's known_hosts.
func trustRigHost(t *testing.T, rig sshRig, key ssh.PublicKey) {
	t.Helper()
	layout, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.EnsureHome(); err != nil {
		t.Fatal(err)
	}
	host := rig.addr[len("mrvl://rig@"):]
	if err := knownhosts.Trust(layout, host, key); err != nil {
		t.Fatal(err)
	}
}

// resetKnownHosts empties the scratch home's known_hosts, so each test
// starts with no recorded host.
func resetKnownHosts(t *testing.T) string {
	t.Helper()
	layout, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.EnsureHome(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.KnownHosts(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return layout.KnownHosts()
}

// TestSSHRigAnswers is the rig's positive control: a trusted host and the
// accepted key get an answer.
func TestSSHRigAnswers(t *testing.T) {
	resetKnownHosts(t)
	client, pemBytes := newSigner(t)
	rig := startSSHRig(t, client.PublicKey())
	trustRigHost(t, rig, rig.hostKey)
	resp, err := SendRequestWith(rig.addr, Request{Method: "ping"},
		DialOptions{Identity: writeIdentity(t, pemBytes), StrictHostKey: true})
	if err != nil {
		t.Fatalf("SendRequestWith: %v", err)
	}
	if string(resp.Result) != `"ok"` {
		t.Fatalf("result = %s, want \"ok\"", resp.Result)
	}
}

func TestUnknownHostKeyIsTyped(t *testing.T) {
	kh := resetKnownHosts(t)
	client, pemBytes := newSigner(t)
	rig := startSSHRig(t, client.PublicKey())
	_, err := SendRequestWith(rig.addr, Request{Method: "ping"},
		DialOptions{Identity: writeIdentity(t, pemBytes), StrictHostKey: true})
	var unk *knownhosts.HostKeyUnknownError
	if !errors.As(err, &unk) {
		t.Fatalf("err = %v (%T), want a *knownhosts.HostKeyUnknownError in its chain", err, err)
	}
	if want := ssh.FingerprintSHA256(rig.hostKey); unk.Fingerprint != want {
		t.Errorf("Fingerprint = %q, want %q", unk.Fingerprint, want)
	}
	if b, rerr := os.ReadFile(kh); rerr != nil || len(b) != 0 {
		t.Errorf("known_hosts = %q, %v; want it still empty", b, rerr)
	}
}

func TestChangedHostKeyIsTyped(t *testing.T) {
	resetKnownHosts(t)
	client, pemBytes := newSigner(t)
	rig := startSSHRig(t, client.PublicKey())
	other, _ := newSigner(t)
	trustRigHost(t, rig, other.PublicKey())
	_, err := SendRequestWith(rig.addr, Request{Method: "ping"},
		DialOptions{Identity: writeIdentity(t, pemBytes), StrictHostKey: true})
	var chg *knownhosts.HostKeyChangedError
	if !errors.As(err, &chg) {
		t.Fatalf("err = %v (%T), want a *knownhosts.HostKeyChangedError in its chain", err, err)
	}
	if want := ssh.FingerprintSHA256(rig.hostKey); chg.Fingerprint != want {
		t.Errorf("Fingerprint = %q, want the offered key %q", chg.Fingerprint, want)
	}
	var unk *knownhosts.HostKeyUnknownError
	if errors.As(err, &unk) {
		t.Errorf("a changed key also reads as unknown: %v", err)
	}
}

func TestAuthRefusedIsTyped(t *testing.T) {
	resetKnownHosts(t)
	accepted, _ := newSigner(t)
	rig := startSSHRig(t, accepted.PublicKey())
	trustRigHost(t, rig, rig.hostKey)
	offered, pemBytes := newSigner(t)
	_, err := SendRequestWith(rig.addr, Request{Method: "ping"},
		DialOptions{Identity: writeIdentity(t, pemBytes), StrictHostKey: true})
	var ref *AuthRefusedError
	if !errors.As(err, &ref) {
		t.Fatalf("err = %v (%T), want an *AuthRefusedError in its chain", err, err)
	}
	want := []string{ssh.FingerprintSHA256(offered.PublicKey())}
	if fmt.Sprint(ref.Offered) != fmt.Sprint(want) {
		t.Errorf("Offered = %v, want %v", ref.Offered, want)
	}
}
