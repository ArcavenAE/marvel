package daemon

import (
	"testing"

	"github.com/arcavenae/marvel/internal/bus"
)

// detailLifecycle is a provider that also offers the bus-typed status detail.
type detailLifecycle struct{ fakeLifecycle }

func (detailLifecycle) BusStatus() bus.Status {
	return bus.Status{Managed: true, URL: "nats://detail", Pidfile: "proven"}
}

// A provider that offers the broker's own detail has it served by the status RPC.
func TestBusStatusServesTheProvidersOwnDetail(t *testing.T) {
	d := newHandlerDaemon(t)
	d.lifecycle = &detailLifecycle{}
	st, ok := d.busStatus()
	if !ok || st.URL != "nats://detail" || st.Pidfile != "proven" {
		t.Errorf("busStatus = %+v, %v, want the provider's detail", st, ok)
	}
}
