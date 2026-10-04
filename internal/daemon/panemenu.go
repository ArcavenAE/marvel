package daemon

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/limitmenu"
)

// The pane-menu source (design 9.3, ruled UL-R2: "pane text may set
// \"limited\""). It is the one exception to "no pane-text scraping": an
// interactive seat whose account has no fresh reading is marked limited when a
// capture of its pane matches the limit-menu sample. It only reads and sets or
// clears a condition. It never sends a key, and a guard test keeps it so.

// paneMenuInterval is how often a seat's pane is captured, to set the
// condition and, while it holds, to see whether the menu is still there.
const paneMenuInterval = time.Minute

// Why a pane-menu condition cleared, as named on session.unlimited.
const (
	paneClearMenuGone     = "menu-gone"
	paneClearActivity     = "activity-advanced"
	paneClearFreshReading = "fresh-reading-below-limit"
	paneClearSessionEnded = "session-ended"
)

// limitMenuSamples holds the captured samples the matcher works from. It is
// empty in production until a real capture is supplied: no samples, no match,
// so the source sets nothing. Tests supply their own.
type limitMenuSamples struct {
	Menus []limitmenu.Sample
	// PostSelection are the screens a seat shows after the second option was
	// chosen. A condition holds while one is on screen.
	PostSelection []limitmenu.Screen
}

// paneMenuState is the per-seat capture cadence, in memory.
type paneMenuState struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// due reports whether the seat may be captured at now and, if so, records it.
func (p *paneMenuState) due(key string, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.last == nil {
		p.last = map[string]time.Time{}
	}
	if t, ok := p.last[key]; ok && now.Sub(t) < paneMenuInterval {
		return false
	}
	p.last[key] = now
	return true
}

func (p *paneMenuState) forget(key string) {
	p.mu.Lock()
	delete(p.last, key)
	p.mu.Unlock()
}

// evaluatePaneMenus sets and clears the pane-menu condition on interactive
// seats. A seat whose condition another source set is left alone.
func (d *Daemon) evaluatePaneMenus(now time.Time) {
	if len(d.limitMenu.Menus) == 0 {
		// No sample: nothing can match, and nothing is captured to find out.
		// A condition already set still ends, below.
		d.clearOrphanedPaneMenus(now)
		return
	}
	for _, sess := range d.store.ListSessions() {
		held := sess.Limit != nil && sess.Limit.Source == api.LimitSourcePaneMenu
		if sess.Limit != nil && !held {
			continue
		}
		if !sess.State.CountsAsAlive() {
			if held {
				d.clearPaneMenu(sess, paneClearSessionEnded, now)
			}
			continue
		}
		if sess.Runtime.Mode != api.RuntimeModeInteractive || sess.PaneID == "" {
			continue
		}
		reading, state := d.accounts.Reading(api.AccountKeyOf(sess), now)
		if held {
			d.holdPaneMenu(sess, reading, state, now)
		} else {
			d.setPaneMenu(sess, state, now)
		}
	}
}

// clearOrphanedPaneMenus ends a pane-menu condition on a session that is gone,
// even when no sample is loaded.
func (d *Daemon) clearOrphanedPaneMenus(now time.Time) {
	for _, sess := range d.store.ListSessions() {
		if sess.Limit != nil && sess.Limit.Source == api.LimitSourcePaneMenu && !sess.State.CountsAsAlive() {
			d.clearPaneMenu(sess, paneClearSessionEnded, now)
		}
	}
}

// setPaneMenu captures a seat that might be on the menu and sets the condition
// when the capture matches. Only a seat with no fresh reading, whose activity
// advisory reads stalled or unknown, is captured, and a working seat never is.
func (d *Daemon) setPaneMenu(sess api.Session, readingState api.ReadingState, now time.Time) {
	if readingState == api.ReadingFresh {
		return
	}
	if sess.ActivityState != api.ActivityUnknown && sess.ActivityState != api.ActivityStalled {
		return
	}
	if !d.paneMenu.due(sess.Key(), now) {
		return
	}
	capture, err := d.capturePane(sess.PaneID)
	if err != nil {
		return
	}
	for _, sample := range d.limitMenu.Menus {
		res := limitmenu.Match(sample, capture)
		if !res.Matched {
			continue
		}
		prov := d.paneMenuProvenance(sess, sample.Version, res.Span, now)
		var set bool
		_ = d.store.UpdateSession(sess.Key(), func(live *api.Session) error {
			if live.Limit != nil {
				return nil // another source got there first
			}
			prov.ActivityAt = live.ContextAt
			live.Condition, live.Limit = api.ConditionLimited, prov
			set = true
			return nil
		})
		if set {
			events.Emit(d.events, events.Event{
				Kind: events.KindSessionLimited, Severity: events.SeverityWarning,
				Workspace: sess.Workspace, Team: sess.Team, Role: sess.Role, Session: sess.Key(),
				Message: prov.Text(),
			})
		}
		return
	}
}

// paneMenuProvenance builds the provenance for a matched capture, reading the
// reset span in the seat's zone when its environment records one.
func (d *Daemon) paneMenuProvenance(sess api.Session, version, span string, now time.Time) *api.LimitProvenance {
	prov := &api.LimitProvenance{
		Source: api.LimitSourcePaneMenu, SampleVersion: version, CapturedAt: now, Span: span,
		ReportedBy: sess.Key(), Account: api.AccountKeyOf(sess).String(),
	}
	until, err := limitmenu.ParseUntil(span, now, sess.Runtime.Env["TZ"], d.hostZone())
	if err != nil {
		prov.UntilNote = untilNote(err)
		return prov
	}
	prov.ResetsAt, prov.Zone = until.Time, until.Zone
	return prov
}

func untilNote(err error) string {
	switch {
	case errors.Is(err, limitmenu.ErrAmbiguous):
		return "the local time occurs twice"
	case errors.Is(err, limitmenu.ErrNonexistent):
		return "the local time does not occur"
	case errors.Is(err, limitmenu.ErrUnknownZone):
		return "the seat's zone is not known"
	default:
		return "the time was not understood"
	}
}

// holdPaneMenu decides whether a held condition ends: first a fresh reading
// below the limit, then the seat's activity advancing, then a capture that
// shows neither the menu nor the post-selection screen.
func (d *Daemon) holdPaneMenu(sess api.Session, reading api.AccountReading, state api.ReadingState, now time.Time) {
	if state == api.ReadingFresh && !hasFullWindow(reading, now) {
		d.clearPaneMenu(sess, paneClearFreshReading, now)
		return
	}
	if !sess.ContextAt.IsZero() && sess.ContextAt.After(sess.Limit.ActivityAt) {
		d.clearPaneMenu(sess, paneClearActivity, now)
		return
	}
	if !d.paneMenu.due(sess.Key(), now) {
		return
	}
	capture, err := d.capturePane(sess.PaneID)
	if err != nil {
		return // cannot see the pane: hold
	}
	for _, m := range d.limitMenu.Menus {
		if limitmenu.Match(m, capture).Matched {
			return
		}
	}
	for _, scr := range d.limitMenu.PostSelection {
		if limitmenu.MatchScreen(scr, capture).Matched {
			return
		}
	}
	d.clearPaneMenu(sess, paneClearMenuGone, now)
}

func hasFullWindow(reading api.AccountReading, now time.Time) bool {
	for _, w := range reading.Windows {
		if w.UsedPercent != nil && *w.UsedPercent >= 100 && w.ResetsAt.After(now) {
			return true
		}
	}
	return false
}

// clearPaneMenu ends the condition and says which rule did, and whether the
// seat left the menu before the reset time the menu named.
func (d *Daemon) clearPaneMenu(sess api.Session, rule string, now time.Time) {
	var prov *api.LimitProvenance
	_ = d.store.UpdateSession(sess.Key(), func(live *api.Session) error {
		if live.Limit == nil || live.Limit.Source != api.LimitSourcePaneMenu {
			return nil
		}
		prov = live.Limit
		live.Condition, live.Limit = "", nil
		return nil
	})
	if prov == nil {
		return
	}
	d.paneMenu.forget(sess.Key())
	base := events.Event{Workspace: sess.Workspace, Team: sess.Team, Role: sess.Role, Session: sess.Key()}
	un := base
	un.Kind, un.Severity = events.KindSessionUnlimited, events.SeverityInfo
	un.Message = fmt.Sprintf("limit cleared (%s): was %s", rule, prov.Text())
	events.Emit(d.events, un)

	switch {
	case rule == paneClearSessionEnded:
		// The seat is gone; there is no resume to report.
	case prov.ResetsAt.IsZero():
		ev := base
		ev.Kind, ev.Severity = events.KindLimitMenuResetUnknown, events.SeverityInfo
		ev.Message = fmt.Sprintf("cleared by %s; the menu's reset time could not be read (%s), so a resume before it cannot be judged", rule, prov.UntilNote)
		events.Emit(d.events, ev)
	case now.Before(prov.ResetsAt):
		ev := base
		ev.Kind, ev.Severity = events.KindLimitMenuResumedBeforeReset, events.SeverityWarning
		ev.Message = fmt.Sprintf("cleared by %s with %s left before the reset the menu named (%s); this resume may have cost money or moved the seat to another account",
			rule, prov.ResetsAt.Sub(now).Round(time.Minute), prov.ResetsAt.UTC().Format(time.RFC3339))
		events.Emit(d.events, ev)
	}
}

func (d *Daemon) hostZone() *time.Location {
	if d.hostLocation != nil {
		return d.hostLocation
	}
	return time.Local
}

func (d *Daemon) capturePane(paneID string) (string, error) {
	if d.paneCapture != nil {
		return d.paneCapture(paneID)
	}
	return d.driver.CapturePane(paneID)
}
