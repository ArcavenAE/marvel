package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/paths"
	"github.com/arcavenae/marvel/internal/service"
)

// dispatchFakeLifecycle is a provider that imports nothing of internal/bus (MS-8 (d),
// attach and status only).
type dispatchFakeLifecycle struct{ st service.Status }

func (f *dispatchFakeLifecycle) Start(context.Context) error { return nil }
func (f *dispatchFakeLifecycle) Stop(bool)                   {}
func (f *dispatchFakeLifecycle) Restart(string) error        { return nil }
func (f *dispatchFakeLifecycle) Reload() error               { return nil }
func (f *dispatchFakeLifecycle) Status() service.Status      { return f.st }

func fakeEntry(name, class, provider, mode string) config.Service {
	return config.Service{Name: name, Class: class, Provider: provider, Mode: mode}
}

func newDispatchDaemon() *Daemon { return &Daemon{} }

func TestEveryRegisteredProviderHasAnAttachArm(t *testing.T) {
	t.Parallel()
	names := service.ProviderNames()
	if len(names) == 0 {
		t.Fatal("service.ProviderNames() is empty, so the check below would pass for nothing")
	}
	for _, n := range names {
		if attachArms[n] == nil {
			t.Errorf("registered provider %q has no arm in attachArms", n)
		}
	}
}

func TestAttachEntriesDispatchesThroughTheProviderMap(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	lc := &dispatchFakeLifecycle{st: service.Status{State: service.StateUp, Ready: true, PID: 42, Restarts: 3, ObservedAt: now}}
	called := 0
	arms := map[string]attachArm{"fake": func(*Daemon, *config.Cluster, *config.Service, paths.Layout) (service.Lifecycle, error) {
		called++
		return lc, nil
	}}
	d := newDispatchDaemon()
	err := d.attachEntries(&config.Cluster{Name: "kinu"}, []config.Service{fakeEntry("f1", "fake-class", "fake", "managed")}, paths.Layout{}, arms)
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatalf("arm called %d times, want 1", called)
	}
	if got := d.services["f1"].lc; got != service.Lifecycle(lc) {
		t.Errorf("d.services[f1].lc = %v, want the arm's Lifecycle", got)
	}
	if got := d.services["f1"].svc.Provider; got != "fake" {
		t.Errorf("d.services[f1].svc.Provider = %q, want fake", got)
	}
}

func TestAttachEntriesKeepsTheLoudDefaultForAProviderWithNoArm(t *testing.T) {
	t.Parallel()
	d := newDispatchDaemon()
	err := d.attachEntries(&config.Cluster{Name: "kinu"}, []config.Service{fakeEntry("z", "fake-class", "zzz", "managed")}, paths.Layout{}, map[string]attachArm{})
	if err == nil || !strings.Contains(err.Error(), `no daemon arm for provider "zzz"`) {
		t.Fatalf("err = %v, want the no daemon arm refusal", err)
	}
}

func TestAnArmWithNoChildIsRecordedAndLeavesTheBusLifecycleNil(t *testing.T) {
	t.Parallel()
	arms := map[string]attachArm{"fake": func(*Daemon, *config.Cluster, *config.Service, paths.Layout) (service.Lifecycle, error) {
		return nil, nil
	}}
	d := newDispatchDaemon()
	if err := d.attachEntries(&config.Cluster{Name: "kinu"}, []config.Service{fakeEntry("a", service.ClassMessageBus, "fake", "adopted")}, paths.Layout{}, arms); err != nil {
		t.Fatal(err)
	}
	got, ok := d.services["a"]
	if !ok || got.lc != nil || got.svc.Mode != "adopted" {
		t.Errorf("d.services[a] = %+v (present %v), want the entry recorded with no Lifecycle", got, ok)
	}
	if d.lifecycle != nil {
		t.Errorf("d.lifecycle = %v, want nil for an arm with no child", d.lifecycle)
	}
}

func TestOneBusPerClusterIsACapOnTheClass(t *testing.T) {
	t.Parallel()
	arm := func(*Daemon, *config.Cluster, *config.Service, paths.Layout) (service.Lifecycle, error) {
		return nil, nil
	}
	arms := map[string]attachArm{"fake-bus-a": arm, "fake-bus-b": arm}
	d := newDispatchDaemon()
	err := d.attachEntries(&config.Cluster{Name: "kinu"}, []config.Service{
		fakeEntry("a", service.ClassMessageBus, "fake-bus-a", "adopted"),
		fakeEntry("b", service.ClassMessageBus, "fake-bus-b", "adopted"),
	}, paths.Layout{}, arms)
	if err == nil || !strings.Contains(err.Error(), "one bus per cluster today") {
		t.Fatalf("err = %v, want the one bus per cluster refusal for two providers of one class", err)
	}
}

// The bus entry's Lifecycle in d.services is the one d.lifecycle holds.
func TestTheBusEntrysLifecycleIsTheOneDLifecycleHolds(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	lc := &dispatchFakeLifecycle{st: service.Status{State: service.StateUp, Ready: true, PID: 7, ObservedAt: now}}
	arms := map[string]attachArm{service.ProviderNATSServer: func(*Daemon, *config.Cluster, *config.Service, paths.Layout) (service.Lifecycle, error) {
		return lc, nil
	}}
	d := newDispatchDaemon()
	if err := d.attachEntries(&config.Cluster{Name: "kinu"}, []config.Service{fakeEntry("bus", service.ClassMessageBus, service.ProviderNATSServer, "managed")}, paths.Layout{}, arms); err != nil {
		t.Fatal(err)
	}
	if d.lifecycle != service.Lifecycle(lc) || d.services["bus"].lc != d.lifecycle {
		t.Errorf("d.lifecycle = %v, d.services[bus].lc = %v, want both the one Lifecycle the bus arm returned", d.lifecycle, d.services["bus"].lc)
	}
}

// The cap is on one class. Two entries of a class with no cap both attach.
func TestAClassWithNoCapAttachesTwoEntries(t *testing.T) {
	t.Parallel()
	arm := func(*Daemon, *config.Cluster, *config.Service, paths.Layout) (service.Lifecycle, error) {
		return nil, nil
	}
	d := newDispatchDaemon()
	err := d.attachEntries(&config.Cluster{Name: "kinu"}, []config.Service{
		fakeEntry("a", "fake-class", "fake", "adopted"),
		fakeEntry("b", "fake-class", "fake", "adopted"),
	}, paths.Layout{}, map[string]attachArm{"fake": arm})
	if err != nil {
		t.Fatalf("a class with no cap refused a second entry: %v", err)
	}
	if len(d.services) != 2 {
		t.Errorf("attached entries = %d, want 2", len(d.services))
	}
}

// A later class whose arm returns a Lifecycle must not take d.lifecycle over:
// stop, restart and reload still act on the bus through it.
func TestANonBusLifecycleLeavesDLifecycleAsTheBuses(t *testing.T) {
	t.Parallel()
	busLC := &dispatchFakeLifecycle{}
	otherLC := &dispatchFakeLifecycle{}
	arms := map[string]attachArm{
		"fake-bus": func(*Daemon, *config.Cluster, *config.Service, paths.Layout) (service.Lifecycle, error) {
			return busLC, nil
		},
		"fake-other": func(*Daemon, *config.Cluster, *config.Service, paths.Layout) (service.Lifecycle, error) {
			return otherLC, nil
		},
	}
	d := newDispatchDaemon()
	err := d.attachEntries(&config.Cluster{Name: "kinu"}, []config.Service{
		fakeEntry("bus", service.ClassMessageBus, "fake-bus", "managed"),
		fakeEntry("other", "fake-class", "fake-other", "managed"),
	}, paths.Layout{}, arms)
	if err != nil {
		t.Fatal(err)
	}
	if d.lifecycle != service.Lifecycle(busLC) {
		t.Errorf("d.lifecycle = %v, want the bus's Lifecycle after a non-bus entry attached", d.lifecycle)
	}
	if d.services["other"].lc != service.Lifecycle(otherLC) {
		t.Errorf("d.services[other].lc = %v, want the other entry's own Lifecycle", d.services["other"].lc)
	}
}
