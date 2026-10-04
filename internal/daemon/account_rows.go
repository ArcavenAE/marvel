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
			if state == api.ReadingFresh && w.UsedPercent != nil {
				r.Observed = int(math.Round(*w.UsedPercent))
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
func (d *Daemon) recordAccountLimits(req api.AccountLimitsRequest, now time.Time) Response {
	sess, err := d.store.AuthenticateAccountReport(req.Session, req.SessionToken)
	if err != nil {
		d.noteRefusedAccountReport(req, err)
		return Response{Error: fmt.Sprintf("account.limits: %v", err)}
	}
	key := api.AccountKeyOf(sess)
	stored := d.accounts.Record(key, req.Windows, sess.Key(), now)
	data, err := json.Marshal(map[string]any{"account": key.String(), "stored": stored})
	if err != nil {
		return Response{Error: fmt.Sprintf("encode account.limits result: %v", err)}
	}
	return Response{Result: data}
}

func (d *Daemon) handleAccountLimits(params json.RawMessage) Response {
	var req api.AccountLimitsRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return Response{Error: fmt.Sprintf("account.limits: %v", err)}
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
