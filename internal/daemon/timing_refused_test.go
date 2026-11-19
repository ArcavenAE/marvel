package daemon

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/timing"
)

// onlyOtherSite fails when a refused invented method minted a site of its own,
// and when the refusal was not the one under test.
func onlyOtherSite(t *testing.T, d *Daemon, names ...string) {
	t.Helper()
	sites := rpcSites(t, d)
	if got := sites[timing.RPCSite("other")].Count; got != uint64(len(names)) {
		t.Errorf("rpc.other Count = %d, want %d", got, len(names))
	}
	for _, n := range names {
		if _, ok := sites[timing.RPCSite(n)]; ok {
			t.Errorf("a refused invented method minted the site %q", timing.RPCSite(n))
		}
	}
}

func dispatchInvented(t *testing.T, d *Daemon, c caller, names ...string) {
	t.Helper()
	for _, n := range names {
		if resp := d.dispatchAs(daemonrpc.Request{Method: n}, c); resp.Error == "" {
			t.Fatalf("%s: no refusal, want one", n)
		}
	}
}

func TestInventedMethodRefusedByScopeIsBucketed(t *testing.T) {
	d := statusDaemon(t)
	dispatchInvented(t, d, caller{scope: ScopeCredentialPush, local: true}, "invented-scope-a", "invented-scope-b")
	onlyOtherSite(t, d, "invented-scope-a", "invented-scope-b")
}

func TestInventedMethodRefusedWhileSuspendedIsBucketed(t *testing.T) {
	d := statusDaemon(t)
	d.suspendHeld.Store(true)
	dispatchInvented(t, d, localCaller(), "invented-hold-a", "invented-hold-b")
	onlyOtherSite(t, d, "invented-hold-a", "invented-hold-b")
}

func TestInventedMethodRefusedWhileAdoptingIsBucketed(t *testing.T) {
	d := statusDaemon(t)
	d.adoptingSince.Store(time.Now().UnixNano())
	dispatchInvented(t, d, localCaller(), "invented-adopt-a", "invented-adopt-b")
	onlyOtherSite(t, d, "invented-adopt-a", "invented-adopt-b")
}

// A method the daemon serves keeps its own site even when a refusal answers it,
// so a held daemon still shows which calls clients are making.
func TestAServedMethodRefusedWhileAdoptingKeepsItsOwnSite(t *testing.T) {
	d := statusDaemon(t)
	d.adoptingSince.Store(time.Now().UnixNano())
	if resp := d.dispatchAs(daemonrpc.Request{Method: "get"}, localCaller()); resp.Error == "" {
		t.Fatal("get was not refused while adopting")
	}
	if got := rpcSites(t, d)[timing.RPCSite("get")].Count; got != 1 {
		t.Errorf("rpc.get Count = %d, want 1", got)
	}
}

// version and events.watch are named by constants. Each is timed under
// its own name and never as other.
func TestConstantCaseMethodsAreTimedUnderTheirOwnName(t *testing.T) {
	for _, m := range []string{MethodVersion, daemonrpc.MethodEventsWatch} {
		d := statusDaemon(t)
		d.dispatchAs(daemonrpc.Request{Method: m}, localCaller())
		sites := rpcSites(t, d)
		if got := sites[timing.RPCSite(m)].Count; got != 1 {
			t.Errorf("%s: rpc.%s Count = %d, want 1 (sites: %v)", m, m, got, sites)
		}
		if got := sites[timing.RPCSite("other")].Count; got != 0 {
			t.Errorf("%s was timed as rpc.other (%d)", m, got)
		}
	}
}

// Odd names an invented method can take: empty, the bucket's own name, and a
// name that already looks like a site. None may mint a site, on any refusal path.
func TestOddInventedNamesAreBucketedOnEveryRefusalPath(t *testing.T) {
	odd := []string{"", "other", "rpc.get"}
	paths := map[string]func(*Daemon) caller{
		"scope":     func(*Daemon) caller { return caller{scope: ScopeCredentialPush, local: true} },
		"suspended": func(d *Daemon) caller { d.suspendHeld.Store(true); return localCaller() },
		"adopting":  func(d *Daemon) caller { d.adoptingSince.Store(time.Now().UnixNano()); return localCaller() },
	}
	for name, setup := range paths {
		d := statusDaemon(t)
		c := setup(d)
		for _, m := range odd {
			d.dispatchAs(daemonrpc.Request{Method: m}, c)
		}
		sites := rpcSites(t, d)
		if got := sites[timing.RPCSite("other")].Count; got != uint64(len(odd)) {
			t.Errorf("%s: rpc.other Count = %d, want %d", name, got, len(odd))
		}
		if len(sites) != 1 {
			t.Errorf("%s: %d sites, want only rpc.other: %v", name, len(sites), sites)
		}
	}
}

// A served method keeps its own site whichever refusal answers it, including the
// adoption branch that defers heartbeat, bus.beat and account.limits.
func TestServedMethodsKeepTheirSiteOnEveryRefusalPath(t *testing.T) {
	d := statusDaemon(t)
	d.adoptingSince.Store(time.Now().UnixNano())
	for _, m := range []string{"heartbeat", "bus.beat", "account.limits"} {
		d.dispatchAs(daemonrpc.Request{Method: m}, localCaller())
	}
	d.adoptingSince.Store(0)
	d.suspendHeld.Store(true)
	d.dispatchAs(daemonrpc.Request{Method: "get"}, localCaller())
	d.suspendHeld.Store(false)
	d.dispatchAs(daemonrpc.Request{Method: "credential.reveal"}, caller{scope: ScopeCredentialPush, local: true})

	sites := rpcSites(t, d)
	for _, m := range []string{"heartbeat", "bus.beat", "account.limits", "get", "credential.reveal"} {
		if got := sites[timing.RPCSite(m)].Count; got != 1 {
			t.Errorf("rpc.%s Count = %d, want 1", m, got)
		}
	}
	if _, ok := sites[timing.RPCSite("other")]; ok {
		t.Error("a served method was bucketed as other")
	}
}
