package daemonrpc

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// The phases of one exchange, as TimeoutError.Phase reports them.
const (
	phaseConnect   = "connect"
	phaseHandshake = "handshake"
	phaseAuth      = "auth"
	phaseReply     = "reply"
)

// defaultMaxReplyBytes bounds one daemon reply. Over it, the reply is a
// BadReplyError that names the limit, so a daemon cannot make the client
// read without end.
//
// The value is a placeholder until the operator sets it from the largest
// real reply (#761).
const defaultMaxReplyBytes int64 = 64 << 20

// maxReplyBytes is the bound in force; tests lower it.
var maxReplyBytes = defaultMaxReplyBytes

// exchange is the per-call timer: armed before the dial, it bounds the
// connect, the handshake, auth, the request and the reply as one span.
// When it fires it closes whatever connection the call holds at that
// moment, which unblocks any read or write on it; a deadline would not, on
// an SSH session, whose SetDeadline does nothing. A nil exchange has no
// timer and does nothing.
type exchange struct {
	timeout  time.Duration
	deadline time.Time
	timer    *time.Timer

	mu     sync.Mutex
	phase  string
	closer io.Closer
	fired  string // the phase the timer fired in; empty until it fires
}

// startExchange arms the timer, or returns nil when d is not positive.
func startExchange(d time.Duration) *exchange {
	if d <= 0 {
		return nil
	}
	ex := &exchange{timeout: d, deadline: time.Now().Add(d), phase: phaseConnect}
	ex.timer = time.AfterFunc(d, ex.fire)
	return ex
}

func (ex *exchange) fire() {
	ex.mu.Lock()
	ex.fired = ex.phase
	c := ex.closer
	ex.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

// enter records the phase the call is in and the connection the timer
// closes if it fires now. If the timer has already fired, c is closed at
// once.
func (ex *exchange) enter(phase string, c io.Closer) {
	if ex == nil {
		return
	}
	ex.mu.Lock()
	ex.phase, ex.closer = phase, c
	fired := ex.fired != ""
	ex.mu.Unlock()
	if fired && c != nil {
		_ = c.Close()
	}
}

// connectTimeout is the time left for a connect, capped at limit when
// limit is positive. Without a timer it is limit unchanged, and zero means
// no timeout.
func (ex *exchange) connectTimeout(limit time.Duration) time.Duration {
	if ex == nil {
		return limit
	}
	left := time.Until(ex.deadline)
	if left <= 0 {
		left = time.Nanosecond
	}
	if limit > 0 && limit < left {
		return limit
	}
	return left
}

// stop disarms the timer once the call is done.
func (ex *exchange) stop() {
	if ex != nil {
		ex.timer.Stop()
	}
}

// wrap turns err into a TimeoutError naming the phase when the timer has
// fired, since the error is then the closed connection the timer caused.
// Any other error, and a TimeoutError already typed, passes through.
func (ex *exchange) wrap(err error) error {
	if ex == nil || err == nil {
		return err
	}
	var tmo *TimeoutError
	if errors.As(err, &tmo) {
		return err
	}
	ex.mu.Lock()
	phase := ex.fired
	ex.mu.Unlock()
	if phase == "" {
		return err
	}
	return &TimeoutError{Phase: phase, Err: fmt.Errorf("no answer within %s, during %s: %w", ex.timeout, phase, err)}
}

// errReplyTooLarge is returned by a boundedReader past its limit.
var errReplyTooLarge = errors.New("reply too large")

// boundedReader reads at most limit bytes, then fails with
// errReplyTooLarge.
type boundedReader struct {
	r     io.Reader
	left  int64
	limit int64
}

func newBoundedReader(r io.Reader, limit int64) *boundedReader {
	return &boundedReader{r: r, left: limit, limit: limit}
}

func (b *boundedReader) Read(p []byte) (int, error) {
	if b.left <= 0 {
		return 0, errReplyTooLarge
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.r.Read(p)
	b.left -= int64(n)
	return n, err
}
