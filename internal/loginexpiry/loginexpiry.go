// Package loginexpiry reads when a harness login expires, by asking the
// harness's own status command, and tracks each login's state so a warning
// is raised once, ahead of the lapse (docs/design/native-checks.md section 2).
//
// It reads no credential. The status command's answer carries an email and an
// organization next to the three fields wanted; Decode keeps the three and
// drops the rest, and no output text reaches an error, an event or a log.
package loginexpiry

import (
	"context"
	"io"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// MaxOutput is the most stdout Ask and Decode read.
const MaxOutput = 64 << 10

// WarnWithin is how far ahead of the expiry the warning is raised.
const WarnWithin = 72 * time.Hour

// Reading is the three fields kept from the status command's answer.
type Reading struct {
	LoggedIn     bool
	ExpiresAt    time.Time
	Subscription string
}

// Reason is why a read gave no reading. The set is fixed so no output text can
// reach an error.
type Reason string

const (
	ReasonTimedOut      Reason = "timed out"
	ReasonExitNonZero   Reason = "exited non-zero"
	ReasonNotJSON       Reason = "not JSON"
	ReasonExpiryMissing Reason = "expiry missing"
)

// ReadError is a read that gave no reading.
type ReadError struct{ Reason Reason }

func (e *ReadError) Error() string { return "login status read failed: " + string(e.Reason) }

// Decode reads the status command's stdout and keeps loginExpiresAt,
// loggedIn and subscriptionType.
func Decode(r io.Reader) (Reading, error) { return Reading{}, nil }

// Ask runs `<program> auth status --json` with CLAUDE_CONFIG_DIR set to
// configDir exactly as given (removed from the environment when configDir is
// empty), bounded by bound, and decodes the answer.
func Ask(ctx context.Context, program, configDir string, bound time.Duration) (Reading, error) {
	return Reading{}, nil
}

// State is where one login stands.
type State string

const (
	StateOK         State = "ok"
	StateExpiring   State = "expiring"
	StateLapsed     State = "lapsed"
	StateUnreadable State = "unreadable"
)

// Row is one login's standing, as a verb or a vitals row shows it.
type Row struct {
	Key       api.AccountKey
	State     State
	ExpiresAt time.Time
	ReadAt    time.Time
	// Why is the fixed reason of the last failed read, empty when it succeeded.
	Why Reason
}

// Tracker holds each login's state and emits an event on each transition.
type Tracker struct{}

// NewTracker returns a tracker that emits to emit.
func NewTracker(emit events.Emitter) *Tracker { return &Tracker{} }

// Observe records one read of one login. err is the read's error, nil when rd
// is a reading.
func (t *Tracker) Observe(key api.AccountKey, rd Reading, err error, now time.Time) {}

// Rows lists every login seen, ordered by key string.
func (t *Tracker) Rows() []Row { return nil }
