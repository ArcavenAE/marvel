package daemon

import (
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// txUsage reads a claude seat's transcript and keeps the seat's output-token
// sum in the spend slice (I-477 slice 1). Skeleton: it reads nothing yet.
type txUsage struct {
	store *api.Store
	// client reports whether a workspace is a client one; such a seat is not
	// read, and nil counts every workspace as client.
	client func(workspace string) (bool, error)
	// tailRead returns a transcript's bytes from an offset to its end.
	tailRead func(path string, from int64) ([]byte, error)
	now      func() time.Time
}

func newTxUsage(store *api.Store, client func(string) (bool, error)) *txUsage {
	return &txUsage{store: store, client: client, tailRead: readTranscriptTail, now: time.Now}
}

// Once runs one pass over every running claude seat.
func (u *txUsage) Once() {}
