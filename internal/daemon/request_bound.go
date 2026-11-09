package daemon

import (
	"errors"
	"io"

	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// maxRequestBytes is the most the daemon reads for one request, the same bound
// the client checks before it dials (marvel#1197). Tests lower it.
var maxRequestBytes = daemonrpc.DefaultMaxRequestBytes

// errRequestTooLarge is what a boundedReader returns once it has handed on
// more than its limit.
var errRequestTooLarge = errors.New("request too large")

// boundedReader reads from r, never reads more than limit+1 bytes through it,
// and fails every read after the first byte past limit. The request is decoded
// before its method is known, so a reader under the decode is the one place a
// limit reaches every method. It returns the error with no data, because
// json.Decoder drops an error that arrives with the bytes of a read, and it
// caps each read at one byte past the limit so the daemon never takes more.
// The count is bytes read, a request's encoding plus whatever the decoder read
// beyond it, so a request that fills the limit exactly can be refused.
type boundedReader struct {
	r     io.Reader
	limit int64
	n     int64
}

// over reports whether the limit has been passed.
func (b *boundedReader) over() bool { return b.n > b.limit }

func (b *boundedReader) Read(p []byte) (int, error) {
	if b.over() {
		return 0, errRequestTooLarge
	}
	if rem := b.limit + 1 - b.n; int64(len(p)) > rem {
		p = p[:rem]
	}
	n, err := b.r.Read(p)
	b.n += int64(n)
	return n, err
}
