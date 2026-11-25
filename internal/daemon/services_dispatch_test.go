package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemonrpc"
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
	rows := d.servicesStatus(now)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	r := rows[0]
	if r.Name != "f1" || r.Provider != "fake" || r.Class != "fake-class" || r.Mode != "managed" {
		t.Errorf("row identity = %+v", r)
	}
	v := r.Reading.Value
	if v.State != "up" || v.Ready == nil || !*v.Ready || v.PID == nil || *v.PID != 42 || v.Restarts == nil || *v.Restarts != 3 {
		t.Errorf("reading = %+v, want up, ready, pid 42, restarts 3", v)
	}
	if r.Reading.Source != "provider:fake" {
		t.Errorf("source = %q, want provider:fake", r.Reading.Source)
	}
	if !r.Reading.ObservedAt.Equal(now) || !r.Reading.ValidUntil.After(r.Reading.ObservedAt) {
		t.Errorf("observed_at %v valid_until %v, want observed_at the provider's and valid_until after it", r.Reading.ObservedAt, r.Reading.ValidUntil)
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

func TestAnAdoptedArmWithNoChildReportsOnlySource(t *testing.T) {
	t.Parallel()
	arms := map[string]attachArm{"fake": func(*Daemon, *config.Cluster, *config.Service, paths.Layout) (service.Lifecycle, error) {
		return nil, nil
	}}
	d := newDispatchDaemon()
	if err := d.attachEntries(&config.Cluster{Name: "kinu"}, []config.Service{fakeEntry("a", "fake-class", "fake", "adopted")}, paths.Layout{}, arms); err != nil {
		t.Fatal(err)
	}
	rows := d.servicesStatus(time.Now().UTC())
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	raw, err := json.Marshal(rows[0])
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Reading struct {
			Value  map[string]any `json:"value"`
			Source string         `json:"source"`
		} `json:"reading"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m.Reading.Source != "provider:fake" {
		t.Errorf("reading = %s, want source provider:fake", raw)
	}
	// State is unmeasured too: the vocabulary is up, down and backoff, and the
	// row's mode already says adopted.
	for _, k := range []string{"state", "pid", "restarts", "ready"} {
		if _, ok := m.Reading.Value[k]; ok {
			t.Errorf("an unmeasured %q is on the wire as a value: %s", k, raw)
		}
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

func TestDaemonStatusServicesRowForTheNATSArmAndBusUnchanged(t *testing.T) {
	sock := writeServicesConfig(t, adoptedBusEntry)
	d := newHandlerDaemon(t)
	if err := d.attachServices(sock); err != nil {
		t.Fatal(err)
	}
	resp := d.dispatchAs(daemonrpc.Request{Method: "daemon.status"}, localCaller())
	if resp.Error != "" {
		t.Fatal(resp.Error)
	}
	var st struct {
		Bus      json.RawMessage `json:"bus"`
		Services []ServiceStatus `json:"services"`
	}
	if err := json.Unmarshal(resp.Result, &st); err != nil {
		t.Fatal(err)
	}
	b, ok := d.busStatus()
	if !ok {
		t.Fatal("no bus status")
	}
	want, _ := json.Marshal(&b)
	if string(st.Bus) != string(want) {
		t.Errorf("bus changed on the wire:\n got %s\nwant %s", st.Bus, want)
	}
	if len(st.Services) != 1 || st.Services[0].Name != "bus" || st.Services[0].Provider != service.ProviderNATSServer ||
		st.Services[0].Reading.Source != "provider:nats-server" {
		t.Errorf("services = %+v, want one bus row sourced provider:nats-server", st.Services)
	}
}

// A managed nats row carries valid_until and the provider source, and the bus
// entry's Lifecycle in d.services is the one d.lifecycle holds.
func TestTheNATSArmRowCarriesValidUntilAndSource(t *testing.T) {
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
	rows := d.servicesStatus(now)
	if len(rows) != 1 || rows[0].Reading.Source != "provider:nats-server" || rows[0].Reading.ValidUntil.IsZero() {
		t.Fatalf("rows = %+v, want one row with source provider:nats-server and a valid_until", rows)
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
	if rows := d.servicesStatus(time.Now().UTC()); len(rows) != 2 {
		t.Errorf("rows = %d, want 2", len(rows))
	}
}
