package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/keys"
	"github.com/arcavenae/marvel/internal/paths"
)

// Characterization tests for the remote half of the daemon client and the
// server half it talks to: SendRequestWith over mrvl:// and over the direct
// form of ssh:// (no path), against a real StartMRVL listener on 127.0.0.1.
// The client's dialers (dialMRVL, dialSSH, dialSSHDirect, sshClientConfig and
// the session conn) and the server's authorizeKey and handleConnection had no
// test that reached them; the restructure's Move 1 moves the client, so these
// pin what it does today. They need no tmux. They do not cover the ssh:// form
// with a path (dialSSHTunnel), which needs an sshd that serves streamlocal
// forwarding.
//
// Not parallel: each test sets HOME, which the key, known_hosts and
// authorized_keys paths read.

const remoteBuildVersion = "0.0.0-remote-client-test"

// remoteFixture is a daemon listening on a loopback port, a client key it
// authorizes, and a scratch HOME that holds both sides' files.
type remoteFixture struct {
	port string
	key  *keys.ClientKey // authorized, admin scope
	home string
}

func newRemoteFixture(t *testing.T) remoteFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MARVEL_HOME", "")
	t.Setenv("SSH_AUTH_SOCK", "")
	t.Setenv("USER", "u")

	layout, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	key := generateKey(t, layout, "client")
	authorize(t, key, ScopeAdmin)

	d := &Daemon{home: "/remote-client-test-home", build: Build{Version: remoteBuildVersion, Channel: "test"}}
	startMRVL(t, d, "127.0.0.1:0")
	_, port, err := net.SplitHostPort(d.sshServer.Load().Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return remoteFixture{port: port, key: key, home: home}
}

func generateKey(t *testing.T, layout paths.Layout, name string) *keys.ClientKey {
	t.Helper()
	k, err := keys.GenerateClient(layout, keys.GenerateOptions{Name: name, Comment: "remote-client-test-" + name})
	if err != nil {
		t.Fatalf("generate key %s: %v", name, err)
	}
	return k
}

func authorize(t *testing.T, k *keys.ClientKey, scope Scope) {
	t.Helper()
	pub, err := os.ReadFile(k.PublicPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := AddAuthorizedKey(pub, "remote-client-test", scope); err != nil {
		t.Fatalf("authorize %s: %v", k.Name, err)
	}
}

func (f remoteFixture) opts(identity string) DialOptions {
	return DialOptions{Identity: identity, TrustUnknownHost: true, Timeout: 20 * time.Second}
}

func versionOf(t *testing.T, resp *Response) BuildInfo {
	t.Helper()
	if resp.Error != "" {
		t.Fatalf("version answered with an error: %s", resp.Error)
	}
	var bi BuildInfo
	if err := json.Unmarshal(resp.Result, &bi); err != nil {
		t.Fatalf("decode version result %s: %v", resp.Result, err)
	}
	return bi
}

func TestRemoteClientVersionOverMRVL(t *testing.T) {
	f := newRemoteFixture(t)
	resp, err := SendRequestWith("mrvl://u@127.0.0.1:"+f.port, Request{Method: MethodVersion}, f.opts(f.key.PrivatePath))
	if err != nil {
		t.Fatalf("SendRequestWith over mrvl://: %v", err)
	}
	if bi := versionOf(t, resp); bi.Version != remoteBuildVersion {
		t.Errorf("version = %q, want %q", bi.Version, remoteBuildVersion)
	}
	if resp.DaemonHome != "/remote-client-test-home" {
		t.Errorf("the response is not stamped with the daemon's home: %q", resp.DaemonHome)
	}
}

func TestRemoteClientVersionOverSSHDirect(t *testing.T) {
	f := newRemoteFixture(t)
	resp, err := SendRequestWith("ssh://u@127.0.0.1:"+f.port, Request{Method: MethodVersion}, f.opts(f.key.PrivatePath))
	if err != nil {
		t.Fatalf("SendRequestWith over ssh:// with a port and no path: %v", err)
	}
	if bi := versionOf(t, resp); bi.Version != remoteBuildVersion {
		t.Errorf("version = %q, want %q", bi.Version, remoteBuildVersion)
	}
}

// The ssh:// form with no path needs the daemon's own SSH port in the URL.
func TestRemoteClientSSHDirectNeedsAPort(t *testing.T) {
	f := newRemoteFixture(t)
	_, err := SendRequestWith("ssh://u@127.0.0.1", Request{Method: MethodVersion}, f.opts(f.key.PrivatePath))
	if err == nil || !strings.Contains(err.Error(), "need a port") {
		t.Errorf("error = %v, want one saying the address needs a port", err)
	}
}

// A first connection with TrustUnknownHost records the daemon's host key in the
// scratch HOME's known_hosts, and that entry is then enough for a strict
// connection. A strict connection to a host that is not recorded is refused
// and records nothing.
func TestRemoteClientHostKeyTrustOnFirstUse(t *testing.T) {
	f := newRemoteFixture(t)
	layout, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	addr := "mrvl://u@127.0.0.1:" + f.port
	strict := DialOptions{Identity: f.key.PrivatePath, StrictHostKey: true, Timeout: 20 * time.Second}

	_, err = SendRequestWith(addr, Request{Method: MethodVersion}, strict)
	if err == nil || !strings.Contains(err.Error(), "is not trusted") {
		t.Fatalf("a strict connection to an unrecorded host: error = %v, want one saying the host key is not trusted", err)
	}
	if fi, statErr := os.Stat(layout.KnownHosts()); statErr == nil && fi.Size() != 0 {
		t.Fatalf("a refused strict connection wrote %d bytes to known_hosts", fi.Size())
	}

	if _, err := SendRequestWith(addr, Request{Method: MethodVersion}, f.opts(f.key.PrivatePath)); err != nil {
		t.Fatalf("the trusting connection: %v", err)
	}
	kh, err := os.ReadFile(layout.KnownHosts())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(kh), "127.0.0.1") || !strings.Contains(string(kh), f.port) {
		t.Errorf("known_hosts has no entry for the daemon's address (%d bytes)", len(kh))
	}
	if filepath.Dir(layout.KnownHosts()) != filepath.Join(f.home, ".marvel") {
		t.Errorf("known_hosts is at %s, not under the scratch HOME's .marvel", layout.KnownHosts())
	}

	resp, err := SendRequestWith(addr, Request{Method: MethodVersion}, strict)
	if err != nil {
		t.Fatalf("a strict connection after the host key was recorded: %v", err)
	}
	if bi := versionOf(t, resp); bi.Version != remoteBuildVersion {
		t.Errorf("version = %q, want %q", bi.Version, remoteBuildVersion)
	}
}

// A key the daemon does not know is refused at the handshake, over both forms.
func TestRemoteClientUnauthorizedKeyIsRefused(t *testing.T) {
	f := newRemoteFixture(t)
	layout, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	stranger := generateKey(t, layout, "stranger")
	for _, addr := range []string{"mrvl://u@127.0.0.1:" + f.port, "ssh://u@127.0.0.1:" + f.port} {
		resp, err := SendRequestWith(addr, Request{Method: MethodVersion}, f.opts(stranger.PrivatePath))
		if err == nil {
			t.Errorf("%s: an unauthorized key got through: %+v", addr, resp)
			continue
		}
		if !strings.Contains(err.Error(), "ssh connect") || !strings.Contains(err.Error(), "authenticate") {
			t.Errorf("%s: error = %v, want an ssh connect failure that names authentication", addr, err)
		}
	}
}

// The scope recorded beside a key in authorized_keys is what the daemon
// enforces for that caller: an admin key may call version and a
// credential-push key may not.
func TestRemoteClientKeyScopeIsEnforced(t *testing.T) {
	f := newRemoteFixture(t)
	layout, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	pusher := generateKey(t, layout, "pusher")
	authorize(t, pusher, ScopeCredentialPush)

	resp, err := SendRequestWith("mrvl://u@127.0.0.1:"+f.port, Request{Method: MethodVersion}, f.opts(pusher.PrivatePath))
	if err != nil {
		t.Fatalf("the credential-push key could not connect: %v", err)
	}
	if !strings.Contains(resp.Error, "is not permitted for a credential-push key") {
		t.Errorf("response error = %q, want the scope refusal for a credential-push key", resp.Error)
	}
	if len(resp.Result) != 0 {
		t.Errorf("a refused method returned a result: %s", resp.Result)
	}
}

// A port nothing listens on is a connection failure the client reports, not a
// hang and not a success.
func TestRemoteClientRefusedPort(t *testing.T) {
	f := newRemoteFixture(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	for _, addr := range []string{"mrvl://u@127.0.0.1:" + port, "ssh://u@127.0.0.1:" + port} {
		_, err := SendRequestWith(addr, Request{Method: MethodVersion}, f.opts(f.key.PrivatePath))
		if err == nil || !strings.Contains(err.Error(), "ssh connect") {
			t.Errorf("%s: error = %v, want an ssh connect failure", addr, err)
		}
	}
}

// With no identity and no agent and no default key, the client refuses before
// it dials, and says how to get one.
func TestRemoteClientWithoutAnyKeyRefusesBeforeDialing(t *testing.T) {
	f := newRemoteFixture(t)
	layout, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	// The fixture's key is named "client", not the default name, and there is
	// no ~/.ssh in the scratch HOME, so nothing is found.
	if _, statErr := os.Stat(layout.DefaultClientKey()); statErr == nil {
		t.Fatalf("the scratch HOME has a default client key at %s", layout.DefaultClientKey())
	}
	_, err = SendRequestWith("mrvl://u@127.0.0.1:"+f.port, Request{Method: MethodVersion}, DialOptions{TrustUnknownHost: true, Timeout: 20 * time.Second})
	if err == nil || !strings.Contains(err.Error(), "no SSH auth available") {
		t.Errorf("error = %v, want one saying no SSH auth is available", err)
	}
}
