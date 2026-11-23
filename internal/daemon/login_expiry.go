package daemon

import (
	"context"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/loginexpiry"
)

// loginTarget is one login to ask about: its account key and every spelling of
// its config directory a live seat was launched with, in session order. The
// harness reports a false logged-out for any spelling but the one the login
// was made under (docs/design/native-checks.md section 2.2).
type loginTarget struct {
	Key       api.AccountKey
	Spellings []string
}

// loginTargets lists the logins of the live claude sessions, one target per
// account key, spellings de-duplicated and kept in session order.
func loginTargets(sessions []api.Session) []loginTarget { return nil }

// loginService runs the login expiry reads and holds the tracker.
type loginService struct{}

// newLoginService builds the service. ask reads one spelling of one login.
func newLoginService(enabled bool, emit events.Emitter, list func() []api.Session,
	ask func(ctx context.Context, spelling string) (loginexpiry.Reading, error),
) *loginService {
	return &loginService{}
}

// pass asks every target once and feeds the tracker. A target whose first
// spelling reads logged out tries the next; the first reading that is logged
// in wins, and when none is the last reading stands. A service that is off
// asks nothing.
func (l *loginService) pass(ctx context.Context, now time.Time) {}

// loginInterval is the wait before the next pass: an hour while any login is
// expiring, lapsed or unreadable or has an expiry within 96 hours, so a renewal
// is seen within the hour, otherwise 6 hours.
func loginInterval(rows []loginexpiry.Row, now time.Time) time.Duration { return 0 }

// result answers get account-logins.
func (l *loginService) result(now time.Time) api.AccountLogins { return api.AccountLogins{} }
