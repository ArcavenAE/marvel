package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/knownhosts"
	"github.com/arcavenae/marvel/internal/paths"
)

// The same-daemon merge (#831, plan section 3.3), on the F and S rigs.

// sdFake is an F rig that also answers version, as a daemon does with its pid
// and start time. Its nth get answers getRows[n]; the last set repeats.
type sdFake struct {
	sock     string
	pid      int
	started  time.Time
	getRows  [][]api.Session
	mu       sync.Mutex
	gets     int
	versions int
}

func startSD(t *testing.T, started time.Time, getRows ...[]api.Session) *sdFake {
	t.Helper()
	dir, err := os.MkdirTemp("", "sd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f := &sdFake{sock: filepath.Join(dir, "d.sock"), pid: 4242, started: started, getRows: getRows}
	ln, err := net.Listen("unix", f.sock)
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
				var req daemonrpc.Request
				if json.NewDecoder(c).Decode(&req) != nil {
					return
				}
				var data []byte
				f.mu.Lock()
				switch req.Method {
				case "version":
					f.versions++
					data, _ = json.Marshal(map[string]any{"version": "x", "started_at": f.started, "pid": f.pid})
				default:
					i := min(f.gets, len(f.getRows)-1)
					f.gets++
					data, _ = json.Marshal(f.getRows[i])
				}
				f.mu.Unlock()
				_ = json.NewEncoder(c).Encode(daemonrpc.Response{Result: data})
			}()
		}
	}()
	return f
}

func (f *sdFake) counts() (gets, versions int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gets, f.versions
}

// sshFront is the S rig: an SSH server on 127.0.0.1:0 that accepts any client
// and serves direct-streamlocal channels, which is what an ssh://host/path
// address asks of an sshd, by dialling the local socket routed for that path.
func sshFront(t *testing.T, routes map[string]string) (addr string, hostKey ssh.PublicKey) {
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
				sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					return
				}
				defer func() { _ = sc.Close() }()
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					if nc.ChannelType() != "direct-streamlocal@openssh.com" {
						_ = nc.Reject(ssh.UnknownChannelType, "no")
						continue
					}
					var p struct {
						Path      string
						Reserved0 string
						Reserved1 uint32
					}
					if ssh.Unmarshal(nc.ExtraData(), &p) != nil || routes[p.Path] == "" {
						_ = nc.Reject(ssh.ConnectionFailed, "no route")
						continue
					}
					up, err := net.Dial("unix", routes[p.Path])
					if err != nil {
						_ = nc.Reject(ssh.ConnectionFailed, err.Error())
						continue
					}
					ch, chReqs, err := nc.Accept()
					if err != nil {
						_ = up.Close()
						continue
					}
					go ssh.DiscardRequests(chReqs)
					go func() { _, _ = io.Copy(up, ch); _ = up.Close() }()
					go func() { _, _ = io.Copy(ch, up); _ = ch.Close() }()
				}
			}()
		}
	}()
	return ln.Addr().String(), signer.PublicKey()
}

// trustHostKey records the key of host:port in the scratch known_hosts, so a
// strict dial verifies it.
func trustHostKey(t *testing.T, hostport string, key ssh.PublicKey) {
	t.Helper()
	layout, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.EnsureHome(); err != nil {
		t.Fatal(err)
	}
	if err := knownhosts.Trust(layout, hostport, key); err != nil {
		t.Fatal(err)
	}
}

func readDoc(t *testing.T, args ...string) (fanOutDocument, int) {
	t.Helper()
	code, out, stderr := runFanGet(t, append([]string{"get", "sessions"}, append(args, "-o", "json")...)...)
	var doc fanOutDocument
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not a document: %v\nstdout: %s\nstderr: %s", err, out, stderr)
	}
	return doc, code
}

func recordOf(t *testing.T, doc fanOutDocument, name string) clusterRecord {
	t.Helper()
	for _, r := range doc.Clusters {
		if r.Cluster == name {
			return r
		}
	}
	t.Fatalf("no record for cluster %s in %+v", name, doc.Clusters)
	return clusterRecord{}
}

func sessionNames(doc fanOutDocument) []string {
	var names []string
	for _, s := range doc.Sessions {
		names = append(names, s.Workspace+"/"+s.Name)
	}
	return names
}

var sdStart = time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)

// Two entries that spell one address dial it once; the second reads
// same_daemon and names the first.
func TestSameAddressDialledOnce(t *testing.T) {
	f := startSD(t, sdStart, []api.Session{row("w", "s1"), row("w", "s2")})
	clusterConfig(t, config.Cluster{Name: "a", Socket: f.sock}, config.Cluster{Name: "b", Socket: f.sock})
	doc, code := readDoc(t, "--cluster", "a,b")
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if got := recordOf(t, doc, "b"); got.Outcome != outcomeSameDaemon || got.SameDaemonAs != "a" {
		t.Errorf("b = %+v, want same_daemon naming a", got)
	}
	if gets, _ := f.counts(); gets != 1 {
		t.Errorf("the address was read %d times, want 1", gets)
	}
	if got := sessionNames(doc); len(got) != 2 {
		t.Errorf("sessions = %v, want each once", got)
	}
}

// Two paths to one socket (one a symlink) are one daemon, once its pid and
// start time agree.
func TestSameInodeMerges(t *testing.T) {
	f := startSD(t, sdStart, []api.Session{row("w", "s1"), row("w", "s2")})
	link := filepath.Join(filepath.Dir(f.sock), "alias.sock")
	if err := os.Symlink(f.sock, link); err != nil {
		t.Fatal(err)
	}
	clusterConfig(t, config.Cluster{Name: "a", Socket: f.sock}, config.Cluster{Name: "b", Socket: link})
	doc, code := readDoc(t, "--cluster", "a,b")
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if got := recordOf(t, doc, "b"); got.Outcome != outcomeSameDaemon || got.SameDaemonAs != "a" {
		t.Errorf("b = %+v, want same_daemon naming a", got)
	}
	if got := sessionNames(doc); len(got) != 2 {
		t.Errorf("sessions = %v, want each once", got)
	}
}

// A daemon can echo another's pid and start time, so two sockets that are not
// one inode never merge on those fields.
func TestEchoedVersionNotMerged(t *testing.T) {
	rows := []api.Session{row("w", "s1")}
	a := startSD(t, sdStart, rows)
	b := startSD(t, sdStart, rows)
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})
	doc, _ := readDoc(t, "--cluster", "a,b")
	for _, n := range []string{"a", "b"} {
		if got := recordOf(t, doc, n); got.Outcome != outcomeAnswered {
			t.Errorf("%s = %+v, want answered", n, got)
		}
	}
	if got := sessionNames(doc); len(got) != 2 {
		t.Errorf("sessions = %v, want one row per cluster", got)
	}
}

// The same host key in front of two different sockets is two daemons, even
// when both report one pid and start time.
func TestSharedHostKeyDifferentSocketNotMerged(t *testing.T) {
	rows := []api.Session{row("w", "s1")}
	a := startSD(t, sdStart, rows)
	b := startSD(t, sdStart, rows)
	addr, key := sshFront(t, map[string]string{"/run/a.sock": a.sock, "/run/b.sock": b.sock})
	id := identityFile(t)
	clusterConfig(t,
		config.Cluster{Name: "a", Server: "ssh://u@" + addr + "/run/a.sock", Identity: id},
		config.Cluster{Name: "b", Server: "ssh://u@" + addr + "/run/b.sock", Identity: id})
	trustHostKey(t, addr, key)
	doc, _ := readDoc(t, "--cluster", "a,b")
	for _, n := range []string{"a", "b"} {
		if got := recordOf(t, doc, n); got.Outcome != outcomeAnswered {
			t.Errorf("%s = %+v, want answered", n, got)
		}
	}
	if got := sessionNames(doc); len(got) != 2 {
		t.Errorf("sessions = %v, want one row per cluster", got)
	}
}

// Two ssh:// entries to one server, with the same verified host key, the same
// socket path, and one pid and start time, are one daemon.
func TestSameHostKeyAndSocketMerges(t *testing.T) {
	f := startSD(t, sdStart, []api.Session{row("w", "s1"), row("w", "s2")})
	addr, key := sshFront(t, map[string]string{"/run/d.sock": f.sock})
	id := identityFile(t)
	clusterConfig(t,
		config.Cluster{Name: "a", Server: "ssh://u1@" + addr + "/run/d.sock", Identity: id},
		config.Cluster{Name: "b", Server: "ssh://u2@" + addr + "/run/d.sock", Identity: id})
	trustHostKey(t, addr, key)
	doc, code := readDoc(t, "--cluster", "a,b")
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if got := recordOf(t, doc, "b"); got.Outcome != outcomeSameDaemon || got.SameDaemonAs != "a" {
		t.Errorf("b = %+v, want same_daemon naming a", got)
	}
	if got := sessionNames(doc); len(got) != 2 {
		t.Errorf("sessions = %v, want each once", got)
	}
}

// A merge keeps a status line for each entry, and never takes a session out of
// view: a session one read saw and the other missed (a spawn between the two
// reads) is still shown.
func TestMergeNeverHidesStatusLine(t *testing.T) {
	f := startSD(t, sdStart,
		[]api.Session{row("w", "s1")},
		[]api.Session{row("w", "s1"), row("w", "s2")})
	link := filepath.Join(filepath.Dir(f.sock), "alias.sock")
	if err := os.Symlink(f.sock, link); err != nil {
		t.Fatal(err)
	}
	clusterConfig(t, config.Cluster{Name: "a", Socket: f.sock}, config.Cluster{Name: "b", Socket: link})
	code, out, stderr := runFanGet(t, "get", "sessions", "--cluster", "a,b")
	if code != 0 {
		t.Errorf("exit = %d, want 0\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "cluster a: ") || !strings.Contains(stderr, "cluster b: same_daemon") {
		t.Errorf("each entry needs its own status line, the second reading same_daemon:\n%s", stderr)
	}
	if !strings.Contains(stderr, "same daemon as a") {
		t.Errorf("the second line does not name the first:\n%s", stderr)
	}
	for _, name := range []string{"s1", "s2"} {
		if n := strings.Count(out, "w/"+name); n != 1 {
			t.Errorf("%s appears %d times, want once:\n%s", name, n, out)
		}
	}
}
