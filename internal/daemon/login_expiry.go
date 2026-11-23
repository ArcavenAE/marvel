package daemon

import (
	"context"
	"log"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/loginexpiry"
)

const (
	// loginReadBound bounds one status call, as the pre-launch login check does.
	loginReadBound = 10 * time.Second
	// loginSlowEvery and loginFastEvery are the waits between passes
	// (docs/design/native-checks.md section 2.3).
	loginSlowEvery = 6 * time.Hour
	loginFastEvery = time.Hour
	// loginNear is how far from an expiry the faster interval applies.
	loginNear = 96 * time.Hour
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
// account key, spellings de-duplicated and kept in session order. A declared
// seat with no live session is the seat-gap check's business, not this one's.
func loginTargets(sessions []api.Session) []loginTarget {
	var out []loginTarget
	at := map[api.AccountKey]int{}
	for _, s := range sessions {
		if s.Runtime.Name != "claude" || !s.State.CountsAsAlive() {
			continue
		}
		key, spelling := api.AccountKeyOf(s), api.ClaudeConfigDirEnv(s)
		i, ok := at[key]
		if !ok {
			i = len(out)
			at[key] = i
			out = append(out, loginTarget{Key: key})
		}
		dup := false
		for _, have := range out[i].Spellings {
			dup = dup || have == spelling
		}
		if !dup {
			out[i].Spellings = append(out[i].Spellings, spelling)
		}
	}
	return out
}

// loginService runs the login expiry reads and holds the tracker.
type loginService struct {
	enabled bool
	tracker *loginexpiry.Tracker
	list    func() []api.Session
	ask     func(ctx context.Context, spelling string) (loginexpiry.Reading, error)
}

// newLoginService builds the service. ask reads one spelling of one login.
func newLoginService(enabled bool, emit events.Emitter, list func() []api.Session,
	ask func(ctx context.Context, spelling string) (loginexpiry.Reading, error),
) *loginService {
	return &loginService{enabled: enabled, tracker: loginexpiry.NewTracker(emit), list: list, ask: ask}
}

// pass asks every target once and feeds the tracker. A target whose first
// spelling reads logged out tries the next; the first reading that is logged
// in wins, and when none is, a logged-out reading stands over a failed read.
// A service that is off asks nothing.
func (l *loginService) pass(ctx context.Context, now time.Time) {
	if l == nil || !l.enabled {
		return
	}
	for _, t := range loginTargets(l.list()) {
		var (
			best    loginexpiry.Reading
			bestErr error
			have    bool
		)
		for _, spelling := range t.Spellings {
			rd, err := l.ask(ctx, spelling)
			if err == nil && rd.LoggedIn {
				best, bestErr, have = rd, nil, true
				break
			}
			if err == nil || !have {
				best, bestErr, have = rd, err, true
			}
		}
		l.tracker.Observe(t.Key, best, bestErr, now)
	}
}

// loginInterval is the wait before the next pass: an hour while any login is
// expiring, lapsed or unreadable or has an expiry within 96 hours, so a renewal
// or a recovery is seen within the hour, otherwise 6 hours.
func loginInterval(rows []loginexpiry.Row, now time.Time) time.Duration {
	for _, r := range rows {
		switch {
		case r.State == loginexpiry.StateExpiring, r.State == loginexpiry.StateLapsed, r.State == loginexpiry.StateUnreadable:
			return loginFastEvery
		case !r.ExpiresAt.IsZero() && r.ExpiresAt.Sub(now) <= loginNear:
			return loginFastEvery
		}
	}
	return loginSlowEvery
}

// result answers get account-logins.
func (l *loginService) result(now time.Time) api.AccountLogins {
	out := api.AccountLogins{Logins: []api.AccountLogin{}}
	if l == nil || !l.enabled {
		return out
	}
	out.Enabled = true
	for _, r := range l.tracker.Rows() {
		row := api.AccountLogin{Account: r.Key.String(), State: string(r.State), ReadAt: r.ReadAt, Why: string(r.Why)}
		if !r.ExpiresAt.IsZero() {
			exp := r.ExpiresAt
			row.ExpiresAt = &exp
			if left := r.ExpiresAt.Sub(now); left > 0 {
				h := int(left.Hours())
				row.HoursLeft = &h
			}
		}
		if !r.FailingSince.IsZero() {
			since := r.FailingSince
			row.FailingSince = &since
		}
		out.Logins = append(out.Logins, row)
	}
	return out
}

// loadLoginExpiry builds the login expiry service from the cluster entry whose
// socket is ours, found the way loadHostVitals finds it. A switch that is off,
// or a config that cannot be read, leaves no service.
func (d *Daemon) loadLoginExpiry(socketPath string) {
	on := false
	if cfg, err := config.Load(); configLoadUsable(err) && cfg != nil {
		on = cfg.ClusterForSocket(socketPath).LoginExpiryOn()
	}
	if !on {
		return
	}
	d.logins = newLoginService(true, d.events, d.store.ListSessions, askClaudeLogin)
}

// askClaudeLogin reads one spelling of one claude login with the claude on the
// daemon's PATH. A harness that is not there reads like one that failed. The
// daemon package may not import os/exec (limitwiring_exec_test.go), so the
// program name goes to loginexpiry, which resolves it.
func askClaudeLogin(ctx context.Context, spelling string) (loginexpiry.Reading, error) {
	return loginexpiry.Ask(ctx, "claude", spelling, loginReadBound)
}

// startLoginExpiry runs the service's passes until ctx ends: once at start,
// then at the interval its own state sets. It starts nothing when the service
// is off.
func (d *Daemon) startLoginExpiry(ctx context.Context) {
	l := d.logins
	if l == nil || !l.enabled {
		return
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		for {
			now := time.Now().UTC()
			l.pass(ctx, now)
			wait := loginInterval(l.tracker.Rows(), now)
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
	}()
	log.Printf("login expiry: on")
}

// accountLogins answers get account-logins; a daemon with no service answers
// that it is off.
func (d *Daemon) accountLogins(now time.Time) api.AccountLogins {
	return d.logins.result(now)
}
