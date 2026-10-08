package daemon

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/bus"
)

// A daemon reads its client config once, at start. When bus.hub.url is added
// to the file afterwards, leaf connect and disconnect still find no hub, and
// the answer has to say why and how to pick the hub up, not only that none is
// declared (marvel#514).
func TestLeafToggleWithoutAHubExplainsTheStartTimeConfig(t *testing.T) {
	for _, method := range []string{"bus.leaf.connect", "bus.leaf.disconnect"} {
		d := newHandlerDaemon(t)
		attachTestBus(t, d, "")
		d.busSup = new(bus.Supervisor) // the handler answers before it uses the broker

		resp := d.dispatchAs(Request{Method: method}, localCaller())
		if resp.Error == "" {
			t.Fatalf("%s with no hub succeeded, want an error", method)
		}
		for _, want := range []string{
			"bus.hub.url",
			"start",
			"marvel daemon reexec",
			"credential",
			"marvel#339",
		} {
			if !strings.Contains(resp.Error, want) {
				t.Errorf("%s error = %q, want it to mention %q", method, resp.Error, want)
			}
		}
	}
}
