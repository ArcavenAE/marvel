package bus

import (
	"net/http"
	"time"

	"github.com/arcavenae/marvel/internal/config"
)

// AdoptedStatus is the status of an adopted broker, whose leaf the daemon
// reads from the broker's loopback monitor with no credential (aae-orc-c6o29).
// Not built yet.
func AdoptedStatus(rb config.ResolvedBus, client *http.Client, now time.Time) Status {
	return Status{}
}
