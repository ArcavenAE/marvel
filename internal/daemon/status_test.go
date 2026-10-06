package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/bus"
	"github.com/arcavenae/marvel/internal/config"
)

// statusDaemon is a handler daemon rooted at a short scratch home, so the SSH
// host key and the config it reads never touch the live fleet's.
func statusDaemon(t *testing.T) *Daemon {
	t.Helper()
	home, err := os.MkdirTemp("", "mst")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv(config.SocketEnv, "")
	return newHandlerDaemon(t)
}

func daemonStatus(t *testing.T, d *Daemon) DaemonStatus {
	t.Helper()
	resp := d.dispatchAs(Request{Method: "daemon.status"}, localCaller())
	if resp.Error != "" {
		t.Fatalf("daemon.status: %s", resp.Error)
	}
	var st DaemonStatus
	if err := json.Unmarshal(resp.Result, &st); err != nil {
		t.Fatalf("decode %s: %v", resp.Result, err)
	}
	return st
}

func startMRVL(t *testing.T, d *Daemon, addr string) {
	t.Helper()
	if err := d.StartMRVL(addr); err != nil {
		t.Fatalf("StartMRVL(%q): %v", addr, err)
	}
	t.Cleanup(func() { d.sshServer.Load().Stop() })
}

// The mrvl:// listener is off when marvel was started without --mrvl.
func TestStatusOffWithoutMRVLFlag(t *testing.T) {
	d := statusDaemon(t)
	st := daemonStatus(t, d)
	if st.MRVL.State != MRVLOff || st.MRVL.Addr != "" {
		t.Errorf("mrvl = %+v, want off with no address", st.MRVL)
	}
}

// The address comes from the listener the daemon bound, so a port chosen by
// the kernel is the port reported.
func TestStatusReportsBoundAddrWhenMRVLStarted(t *testing.T) {
	d := statusDaemon(t)
	startMRVL(t, d, "127.0.0.1:0")

	st := daemonStatus(t, d)
	if st.MRVL.State != MRVLLoopback {
		t.Errorf("state = %q, want %q", st.MRVL.State, MRVLLoopback)
	}
	bound := d.sshServer.Load().Addr()
	if bound == nil {
		t.Fatal("the SSH server publishes no bound address")
	}
	want := bound.String()
	if st.MRVL.Addr != want {
		t.Errorf("addr = %q, want the bound %q", st.MRVL.Addr, want)
	}
	if _, port, err := net.SplitHostPort(st.MRVL.Addr); err != nil || port == "0" || port == "" {
		t.Errorf("addr %q does not carry the port the kernel chose (%v)", st.MRVL.Addr, err)
	}
}

// A wildcard bind reaches the network, including the bare --mrvl default,
// which has no host at all.
func TestStatusWildcardBindIsNetwork(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", ":0"} {
		t.Run(addr, func(t *testing.T) {
			d := statusDaemon(t)
			startMRVL(t, d, addr)
			if got := daemonStatus(t, d).MRVL.State; got != MRVLNetwork {
				t.Errorf("state for a bind of %q = %q, want %q", addr, got, MRVLNetwork)
			}
		})
	}
}

// classifyBind reads the address, not the flag text: a non-loopback host
// address is network, and a loopback one is loopback.
func TestClassifyBind(t *testing.T) {
	cases := []struct {
		addr string
		want MRVLState
	}{
		{"127.0.0.1:6785", MRVLLoopback},
		{"[::1]:6785", MRVLLoopback},
		{"0.0.0.0:6785", MRVLNetwork},
		{"[::]:6785", MRVLNetwork},
		{"192.0.2.10:6785", MRVLNetwork},
	}
	for _, tc := range cases {
		a, err := net.ResolveTCPAddr("tcp", tc.addr)
		if err != nil {
			t.Fatal(err)
		}
		if got := classifyBind(a); got != tc.want {
			t.Errorf("classifyBind(%s) = %q, want %q", tc.addr, got, tc.want)
		}
	}
	if got := classifyBind(nil); got != MRVLOff {
		t.Errorf("classifyBind(nil) = %q, want %q", got, MRVLOff)
	}
}

// The status names the daemon: its home and the cluster its own config gives
// it.
func TestStatusReportsDaemonIdentity(t *testing.T) {
	d := statusDaemon(t)
	d.cluster = "testcluster"

	st := daemonStatus(t, d)
	if st.Home == "" || st.Home != d.home {
		t.Errorf("home = %q, want the daemon's own %q", st.Home, d.home)
	}
	if st.Cluster != "testcluster" {
		t.Errorf("cluster = %q, want testcluster", st.Cluster)
	}
}

// The daemon learns its cluster name from the config entry whose socket it
// listens on, not from what a client calls it.
func TestIdentifyClusterFromTheConfigEntryForItsSocket(t *testing.T) {
	d := statusDaemon(t)
	sock := filepath.Join(os.TempDir(), "marvel-ident.sock")
	cfg := &config.Config{
		Clusters: []config.Cluster{
			{Name: "elsewhere", Socket: "/scratch/other.sock"},
			{Name: "mine", Socket: sock},
		},
		CurrentCluster: "elsewhere",
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	d.identifyCluster(sock)
	if d.cluster != "mine" {
		t.Errorf("cluster = %q, want mine", d.cluster)
	}

	d.cluster = ""
	d.identifyCluster("/scratch/unknown.sock")
	if d.cluster != "" {
		t.Errorf("cluster = %q for a socket no entry names, want empty", d.cluster)
	}
}

// The status carries the same bus.Status value bus.status returns, so the
// header and `describe daemon` need one call.
func TestStatusEmbedsTheBusStatus(t *testing.T) {
	d := statusDaemon(t)
	if st := daemonStatus(t, d); st.Bus != nil {
		t.Errorf("bus = %+v with no bus configured, want it absent", st.Bus)
	}

	d.sessMgr.Bus = bus.NewAdopted(config.ResolvedBus{Mode: "adopted", Class: "message-bus", Provider: "nats-server", URL: "nats://h:4222"})
	st := daemonStatus(t, d)
	if st.Bus == nil {
		t.Fatal("bus absent, want the adopted bus status")
	}
	resp := d.dispatchAs(Request{Method: "bus.status"}, localCaller())
	var want bus.Status
	if err := json.Unmarshal(resp.Result, &want); err != nil {
		t.Fatal(err)
	}
	if *st.Bus != want {
		t.Errorf("embedded bus = %+v, want what bus.status returns, %+v", *st.Bus, want)
	}
}

// daemon.status is an admin read: a credential-push key does not get it.
func TestDaemonStatusIsNotACredentialPushMethod(t *testing.T) {
	d := statusDaemon(t)
	resp := d.dispatchAs(Request{Method: "daemon.status"}, caller{scope: ScopeCredentialPush, fingerprint: "SHA256:x"})
	if resp.Error == "" || !strings.Contains(resp.Error, "not permitted") {
		t.Fatalf("credential-push key reached daemon.status: %+v", resp)
	}
}

// When two entries name the same socket the daemon cannot tell which one a
// client came in through, so it reports no name rather than the first.
func TestIdentifyClusterReportsNoNameWhenTwoEntriesShareASocket(t *testing.T) {
	d := statusDaemon(t)
	sock := filepath.Join(os.TempDir(), "marvel-ident-shared.sock")
	cfg := &config.Config{
		Clusters: []config.Cluster{
			{Name: "first", Socket: sock},
			{Name: "second", Socket: sock},
		},
		CurrentCluster: "first",
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	d.identifyCluster(sock)
	if d.cluster != "" {
		t.Errorf("cluster = %q, want no name for a socket two entries share", d.cluster)
	}
}

// A socketless entry resolves to the default socket, so it collides with an
// entry that writes that path out: no name either.
func TestIdentifyClusterReportsNoNameForASocketlessEntryAndAnAliasAtTheDefault(t *testing.T) {
	d := statusDaemon(t)
	def := config.DefaultSocket()
	cfg := &config.Config{
		Clusters: []config.Cluster{
			{Name: "local"},
			{Name: "alias", Socket: def},
		},
		CurrentCluster: "local",
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	d.identifyCluster(def)
	if d.cluster != "" {
		t.Errorf("cluster = %q, want no name when %q and %q both resolve to %s", d.cluster, "local", "alias", def)
	}
}

// One match among several entries is still reported.
func TestIdentifyClusterStillReportsASingleMatchAmongOthers(t *testing.T) {
	d := statusDaemon(t)
	sock := filepath.Join(os.TempDir(), "marvel-ident-single.sock")
	cfg := &config.Config{
		Clusters: []config.Cluster{
			{Name: "other", Socket: "/scratch/other.sock"},
			{Name: "remote", Server: "mrvl://example.invalid"},
			{Name: "mine", Socket: sock},
		},
		CurrentCluster: "other",
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	d.identifyCluster(sock)
	if d.cluster != "mine" {
		t.Errorf("cluster = %q, want mine", d.cluster)
	}
}

// strAddr is an address that is not a *net.TCPAddr, so classifyBind has to
// read its text.
type strAddr string

func (strAddr) Network() string  { return "test" }
func (a strAddr) String() string { return string(a) }

// classifyBind reads a non-TCP address by its text: a loopback host is
// loopback, and anything it cannot read as a host and port, or a host that is
// not an IP, is network, never local.
func TestClassifyBindReadsANonTCPAddressByItsText(t *testing.T) {
	cases := []struct {
		addr string
		want MRVLState
	}{
		{"127.0.0.1:6785", MRVLLoopback},
		{"[::1]:6785", MRVLLoopback},
		{"0.0.0.0:6785", MRVLNetwork},
		{"192.0.2.10:6785", MRVLNetwork},
		{"no-port", MRVLNetwork},
		{"somehost:6785", MRVLNetwork},
		{":6785", MRVLNetwork},
	}
	for _, tc := range cases {
		if got := classifyBind(strAddr(tc.addr)); got != tc.want {
			t.Errorf("classifyBind(%q) = %q, want %q", tc.addr, got, tc.want)
		}
	}
}
