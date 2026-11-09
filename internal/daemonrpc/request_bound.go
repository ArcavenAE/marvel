package daemonrpc

import (
	"errors"
	"fmt"
)

// DefaultMaxRequestBytes is the most one request may encode to: 24 MiB, the
// size the cp design names. The daemon refuses a request that passes it
// (marvel#1197) and the client refuses one before it dials. The largest request
// measured is about 0.38 MiB, so every method fits with room to spare. The
// reply bound is a separate one, maxReplyBytes.
const DefaultMaxRequestBytes int64 = 24 << 20

// maxRequestBytes is the client's bound in force; tests lower it.
var maxRequestBytes = DefaultMaxRequestBytes

// ErrRequestTooLarge is what SendRequestWith returns for a request whose
// encoding passes the bound, before any dial.
var ErrRequestTooLarge = errors.New("request too large")

// BoundText prints a byte bound the way an error names it: whole MiB as
// "24 MiB", whole KiB as "1 KiB", anything else in bytes.
func BoundText(n int64) string {
	switch {
	case n > 0 && n%(1<<20) == 0:
		return fmt.Sprintf("%d MiB", n>>20)
	case n > 0 && n%(1<<10) == 0:
		return fmt.Sprintf("%d KiB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}
