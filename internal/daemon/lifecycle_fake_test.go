package daemon

import (
	"context"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/service"
)

// fakeLifecycle is a provider the daemon drives with no bus in sight.
type fakeLifecycle struct {
	restarts []string
	stops    []bool
	status   service.Status
}

func (f *fakeLifecycle) Start(context.Context) error { return nil }
func (f *fakeLifecycle) Stop(keep bool)              { f.stops = append(f.stops, keep) }
func (f *fakeLifecycle) Restart(reason string) error {
	f.restarts = append(f.restarts, reason)
	return nil
}
func (f *fakeLifecycle) Reload() error          { return nil }
func (f *fakeLifecycle) Status() service.Status { return f.status }

// fingerprintingLifecycle also reports an environment fingerprint.
type fingerprintingLifecycle struct {
	fakeLifecycle
	fp  string
	set bool
}

func (f *fingerprintingLifecycle) EnvFingerprint(string) (string, bool) { return f.fp, f.set }

func TestRestartBusAsksTheLifecycle(t *testing.T) {
	d := newHandlerDaemon(t)
	f := &fakeLifecycle{}
	d.lifecycle = f
	d.restartBus("picking up a change")
	if len(f.restarts) != 1 || f.restarts[0] != "picking up a change" {
		t.Errorf("restarts = %v, want the one reason", f.restarts)
	}
}

func TestStopBusKeepsTheChildOnlyWhenHandingOver(t *testing.T) {
	cases := []struct {
		keep, teardown, want bool
	}{
		{keep: true, teardown: false, want: true},
		{keep: true, teardown: true, want: false},
		{keep: false, teardown: false, want: false},
	}
	for _, c := range cases {
		d := newHandlerDaemon(t)
		f := &fakeLifecycle{}
		d.lifecycle = f
		d.keepBus = c.keep
		d.stopBus(c.teardown)
		if len(f.stops) != 1 || f.stops[0] != c.want {
			t.Errorf("keepBus=%v teardown=%v: stops = %v, want one stop with keep=%v", c.keep, c.teardown, f.stops, c.want)
		}
	}
	d := newHandlerDaemon(t)
	d.stopBus(false) // no lifecycle: nothing to stop, no panic
}

// The daemon compares fingerprints: a child started without the seat's value
// needs a restart, one holding a different value needs one, and a provider that
// cannot report a fingerprint is left to a reload.
func TestLeafSeedNeedsRestartAsksTheFingerprinter(t *testing.T) {
	put := func(d *Daemon, seed string) {
		t.Helper()
		if err := d.store.CreateCredential(&api.Credential{Name: busLeafCredential, Kind: api.CredentialNATSNKeySeed, Value: []byte(seed)}); err != nil {
			t.Fatalf("put: %v", err)
		}
	}
	t.Run("child started without the value", func(t *testing.T) {
		d := newHandlerDaemon(t)
		d.lifecycle = &fingerprintingLifecycle{set: false}
		if !d.leafSeedNeedsRestart() {
			t.Error("a child started without the value needs a restart")
		}
	})
	t.Run("different value", func(t *testing.T) {
		d := newHandlerDaemon(t)
		d.lifecycle = &fingerprintingLifecycle{fp: "not-the-stored-seed's-fingerprint", set: true}
		put(d, "SUSEEDFORTEST")
		if !d.leafSeedNeedsRestart() {
			t.Error("a child holding a different value needs a restart")
		}
	})
	t.Run("nothing stored to compare", func(t *testing.T) {
		d := newHandlerDaemon(t)
		d.lifecycle = &fingerprintingLifecycle{fp: "x", set: true}
		if d.leafSeedNeedsRestart() {
			t.Error("with nothing stored a reload is the safe default")
		}
	})
	t.Run("provider cannot report a fingerprint", func(t *testing.T) {
		d := newHandlerDaemon(t)
		d.lifecycle = &fakeLifecycle{}
		put(d, "SUSEEDFORTEST")
		if d.leafSeedNeedsRestart() {
			t.Error("a provider with no fingerprint is left to a reload")
		}
	})
}

// A provider that reports only the plain status still answers the status RPC.
func TestBusStatusFromAPlainLifecycle(t *testing.T) {
	d := newHandlerDaemon(t)
	d.lifecycle = &fakeLifecycle{status: service.Status{State: service.StateUp, Ready: true, PID: 42, Restarts: 3}}
	st, ok := d.busStatus()
	if !ok {
		t.Fatal("busStatus reported no bus with a lifecycle attached")
	}
	if !st.Managed || !st.Ready || st.PID != 42 || st.Restarts != 3 {
		t.Errorf("busStatus = %+v, want managed, ready, pid 42, 3 restarts", st)
	}
}
