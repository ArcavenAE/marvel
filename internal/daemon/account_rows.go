package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/arcavenae/marvel/internal/admission"
	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/events"
)

// harnessRuntimes are the registered harness runtimes, whose sessions spend
// against an account. The value says whether marvel has a way to learn that
// account's limits (finding-050): claude through its statusline and codex
// through its rollout do; forestage and opencode are not verified to. The
// test runtimes (generic, simulator, and a bare command such as sleep) are not
// harnesses and have no account, so they get no row.
//
// An account with live harness sessions always gets a row, because "none"
// means no reading has been received and is true whether or not a source
// exists. The note tells the two apart.
var harnessRuntimes = map[string]bool{
	"claude":    true,
	"codex":     true,
	"forestage": false,
	"opencode":  false,
}

const (
	noteNoReading     = "no reading received for this account"
	noteUnverifiedSrc = "no reading received; this harness is not known to report limits"
)

// accountRows builds the `get budgets` rows for accounts: one row per window
// for an account with a reading (fresh or stale), and a single row with window
// "-" for an account that has live sessions and no reading. The set of
// accounts is those with a stored reading plus those with a live session, so
// "no number" is shown and never left out. Informs, never gates: nothing here
// refuses anything (diagnostic-not-gate).
func accountRows(readings *api.AccountReadings, sessions []api.Session, now time.Time) []admission.Row {
	return accountRowsAt(readings, sessions, now, 0)
}

// accountRowsAt is accountRows with the cluster's warning level: a fresh
// window at or over it reads near-limit in STATE. Level 0 is off.
func accountRowsAt(readings *api.AccountReadings, sessions []api.Session, now time.Time, warnLevel int) []admission.Row {
	seen := map[api.AccountKey]bool{}
	unverified := map[api.AccountKey]bool{}
	var keys []api.AccountKey
	add := func(k api.AccountKey) {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for _, k := range readings.Keys() {
		add(k)
	}
	for i := range sessions {
		if _, harness := harnessRuntimes[sessions[i].Runtime.Name]; harness && sessions[i].State.CountsAsAlive() {
			add(api.AccountKeyOf(sessions[i]))
			if !harnessRuntimes[sessions[i].Runtime.Name] {
				unverified[api.AccountKeyOf(sessions[i])] = true
			}
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })

	var out []admission.Row
	for _, k := range keys {
		reading, state := readings.Reading(k, now)
		base := admission.Row{
			Workspace: "-", Team: "-", Dimension: api.DimAccountWindow,
			State: "-", Account: k.String(), Reading: string(state),
		}
		if state == api.ReadingNone {
			base.AccountWindow = "-"
			base.Note = noteNoReading
			if unverified[k] {
				base.Note = noteUnverifiedSrc
			}
			out = append(out, base)
			continue
		}
		wins := append([]api.AccountWindow(nil), reading.Windows...)
		sort.Slice(wins, func(i, j int) bool { return wins[i].Name < wins[j].Name })
		for _, w := range wins {
			r := base
			r.AccountWindow = w.Name
			if !w.ResetsAt.IsZero() {
				t := w.ResetsAt
				r.ResetsAt = &t
			}
			if w.UsedPercent != nil {
				// Observed is the rounded value for a client that reads only it;
				// ObservedPercent is the raw value, which STATE compares to the level.
				raw := *w.UsedPercent
				r.Observed = int(math.Round(raw))
				r.ObservedPercent = &raw
				switch {
				case state == api.ReadingStale:
					// The last value and how old it is, on this clock; STATE says the
					// reading is stale, whatever the value was.
					r.State = string(api.ReadingStale)
					r.ReadingAgeSeconds = int64(now.Sub(reading.At).Seconds())
				case warnLevel > 0 && *w.UsedPercent >= float64(warnLevel):
					r.State = accountWarnState
				}
			}
			r.Note = fmt.Sprintf("reading from %s at %s", reading.Session, reading.At.UTC().Format(time.RFC3339))
			out = append(out, r)
		}
	}
	return out
}

// recordAccountLimits stores one reading reported by a session. The reporter
// must hold the token minted for the session it names, and a session with no
// token is refused: a reading can mark every session of an account limited.
// The account key is derived from that session's own record, so a reporter
// cannot name an account it is not on. The clock is the daemon's: a sender's
// own time is not trusted to decide what is stale.
func (d *Daemon) recordAccountLimits(req api.AccountLimitsRequest, now time.Time) daemonrpc.Response {
	sess, err := d.store.AuthenticateAccountReport(req.Session, req.SessionToken)
	if err != nil {
		d.noteRefusedAccountReport(req, err)
		return daemonrpc.Response{Error: fmt.Sprintf("account.limits: %v", err)}
	}
	key := api.AccountKeyOf(sess)
	var stored bool
	if req.ObservedAt.IsZero() {
		// No observation time is an unknown one, not "now": it must not displace a
		// stamped reading (a null-field figure posted after a fresh full window).
		stored = d.accounts.RecordUnstamped(key, req.Windows, sess.Key(), now)
	} else {
		observed, err := observationTime(req.ObservedAt, now)
		if err != nil {
			return daemonrpc.Response{Error: fmt.Sprintf("account.limits: %v", err)}
		}
		stored = d.accounts.RecordObserved(key, req.Windows, sess.Key(), observed, now)
	}
	if stored {
		// The history keeps every stored reading with the time it was observed.
		// A reading with no observation time is stamped with the daemon's clock.
		at := now
		if !req.ObservedAt.IsZero() {
			at, _ = observationTime(req.ObservedAt, now)
		}
		d.accountSeries.SetCluster(d.cluster)
		d.accountSeries.Append(key, req.Windows, sess.Key(), at)
		// Only a reading that is current says anything about the window.
		if reading, state := d.accounts.Reading(key, now); state == api.ReadingFresh {
			d.accountWarn.observe(key, sess.Workspace, reading.Windows, now)
		}
	}
	return respond(map[string]any{"account": key.String(), "stored": stored})
}

// accountObservationSkew is how far ahead of the daemon's clock a sender's
// observation time may be before it is refused; within it, it is clamped to now.
const accountObservationSkew = time.Minute

// observationTime resolves a stamped request's observed_at. A time past
// the skew allowance is refused, since a future observation would outlive every
// real one; within it, it is clamped to now.
func observationTime(observed, now time.Time) (time.Time, error) {
	switch {
	case observed.After(now.Add(accountObservationSkew)):
		return time.Time{}, fmt.Errorf("observed_at %s is in the future", observed.UTC().Format(time.RFC3339))
	case observed.After(now):
		return now, nil
	}
	return observed.UTC(), nil
}

func (d *Daemon) handleAccountLimits(params json.RawMessage) daemonrpc.Response {
	var req api.AccountLimitsRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return daemonrpc.Response{Error: fmt.Sprintf("account.limits: %v", err)}
	}
	return d.recordAccountLimits(req, time.Now().UTC())
}

// noteRefusedAccountReport says why a reading was refused, on the ring and in
// the log with the heartbeat's throttle, since the sender is best effort and
// would otherwise fail silently. An unknown session is not reported: it names
// nothing to filter by.
func (d *Daemon) noteRefusedAccountReport(req api.AccountLimitsRequest, err error) {
	var cause, msg string
	switch {
	case errors.Is(err, api.ErrAccountReportUnbound):
		cause, msg = "account-unbound", "account.limits refused: the session record carries no token, so the reading cannot be bound to it; restart it"
	case errors.Is(err, api.ErrHeartbeatUnauthorized) && req.SessionToken == "":
		cause, msg = "account-no-token", "account.limits refused: no token presented while the session carries one, so its producer is not sending MARVEL_HEARTBEAT_TOKEN"
	case errors.Is(err, api.ErrHeartbeatUnauthorized):
		cause, msg = "account-stale-token", "account.limits refused: the token does not match the session named; see 'marvel orphans'"
	default:
		return
	}
	d.emitHeartbeatAuth2(events.KindHeartbeatRefused, cause, req.Session, msg)
}

// accountKeyOfSession resolves the account a session spends against, for a
// caller that holds the token minted for that session. The history is an
// account's, so the caller proves a seat on it the way account.limits does:
// a session of another account, a wrong or missing token, and a record with no
// token are refused.
func (d *Daemon) accountKeyOfSession(name, token string) (api.AccountKey, error) {
	sess, err := d.store.AuthenticateAccountReport(name, token)
	if err != nil {
		return api.AccountKey{}, err
	}
	return api.AccountKeyOf(sess), nil
}

// handleAccountSeries answers the stamped readings of one window of the
// account of a session the caller holds the token of. It reads the history only; it asks nothing of any
// harness.
func (d *Daemon) handleAccountSeries(params json.RawMessage) daemonrpc.Response {
	var req api.AccountSeriesRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return daemonrpc.Response{Error: fmt.Sprintf("account.series: %v", err)}
	}
	key, err := d.accountKeyOfSession(req.Session, req.SessionToken)
	if err != nil {
		return daemonrpc.Response{Error: fmt.Sprintf("account.series: %v", err)}
	}
	d.accountSeries.SetCluster(d.cluster)
	samples := d.accountSeries.Samples(key, req.Window, req.From, req.To)
	if samples == nil {
		samples = []api.AccountSample{}
	}
	return respond(map[string]any{"account": key.String(), "window": req.Window, "samples": samples})
}

// handleAccountDelta answers what a run did to one window: the used percentage
// at its start and end and the difference, unknown where no reading is near.
func (d *Daemon) handleAccountDelta(params json.RawMessage) daemonrpc.Response {
	var req api.AccountDeltaRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return daemonrpc.Response{Error: fmt.Sprintf("account.delta: %v", err)}
	}
	key, err := d.accountKeyOfSession(req.Session, req.SessionToken)
	if err != nil {
		return daemonrpc.Response{Error: fmt.Sprintf("account.delta: %v", err)}
	}
	d.accountSeries.SetCluster(d.cluster)
	return respond(d.accountSeries.Delta(key, req.Window, req.Start, req.End))
}
