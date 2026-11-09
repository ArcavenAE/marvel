package daemon

import (
	"context"
	"time"

	"github.com/arcavenae/marvel/contracts/go/envelope"
	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// accountWarner reports an account window reaching the cluster's warning level,
// once per window. It reads and reports; nothing pauses, sheds or refuses on it.
type accountWarner struct{}

// newAccountWarner builds the warner. level 0 is off. send publishes one
// envelope to the director's inbox; leafUp says whether it may be tried.
func newAccountWarner(level int, em events.Emitter, send func(ctx context.Context, env envelope.Envelope, msgID string) error, leafUp func() bool) *accountWarner {
	return &accountWarner{}
}

// observe is called with each reading just stored for key, on the daemon's clock.
func (w *accountWarner) observe(key api.AccountKey, workspace string, windows []api.AccountWindow, now time.Time) {
}
