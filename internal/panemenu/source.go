// Package panemenu is the pane-menu source of the "limited" condition (design
// 9.3, ruled UL-R2). It is its own package so that it cannot send a key by
// construction: it is handed a function that captures a pane and a function
// that says whether the harness is in front, and nothing else that reaches a
// pane. It holds no tmux driver, no daemon and no inject path, and a guard test
// reads its imports.
package panemenu

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

// Samples holds the captured samples the matcher works from. It is empty in
// production until a real capture is supplied: no samples, no match, so the
// source sets nothing. Tests supply their own.
type Samples struct {
	Menus []limitmenu.Sample
	// PostSelection are the screens a seat shows after the second option was
	// chosen. A condition holds while one is on screen.
	PostSelection []limitmenu.Screen
	// Selections are the P-UL7 measurements of what the digit does on the menu
	// (design 9.7). The limit action reads them; the source also holds a
	// condition while any selection's After screen is on show.
	Selections []limitmenu.Selection
	// OptionOne are screens a seat shows after option 1 was chosen, which a
	// confirmation must never mistake for the second option being taken.
	OptionOne []limitmenu.Screen
}

// Hooks are the optional callbacks a caller hangs behind the source. They get
// facts and nothing else: the source passes no pane, no driver and no way to
// reach one, so a hook can act only with whatever its owner already holds.
type Hooks struct {
	// Matched is called once, after the source set the condition from a menu.
	Matched func(sess api.Session, sample limitmenu.Sample, res limitmenu.Result, now time.Time)
	// Seen is called for each sample a capture did not match, with the matcher's
	// verdict: a refusal means a menu block was found and declined.
	Seen func(sess api.Session, sample limitmenu.Sample, res limitmenu.Result)
	// Cleared is called after a pane-menu condition ended, with the rule.
	Cleared func(sess api.Session, rule string)
}

// heldScreens are the screens a held condition stays up on: the post-selection
// screens and each measurement's own After screen.
func (s Samples) heldScreens() []limitmenu.Screen {
	out := append([]limitmenu.Screen(nil), s.PostSelection...)
	for _, sel := range s.Selections {
		out = append(out, sel.After)
	}
	return out
}

// Store is the part of the session store the source reads and writes.
type Store interface {
	ListSessions() []api.Session
	UpdateSession(key string, fn func(*api.Session) error) error
}

// Readings is the part of the account readings the source reads.
type Readings interface {
	Reading(key api.AccountKey, now time.Time) (api.AccountReading, api.ReadingState)
}

// Config is everything the source is built from. Capture returns the visible
// screen of a pane with wrapped rows joined; InFront says whether the session's
// harness is the process in front of its pane. Those two functions are the whole
// of its reach into a pane.
type Config struct {
	Samples  Samples
	Store    Store
	Readings Readings
	Events   events.Emitter
	Capture  func(paneID string) (string, error)
	InFront  func(sess api.Session) bool
	Hooks    Hooks
	// HostZone is the zone a reset time is read in when the seat records none.
	// Nil means the host's own.
	HostZone *time.Location
}

// Evaluator is all a caller can do with a built source: run an evaluation. The
// type behind it is unexported and has no way back to its fields, so after New
// returns nobody can swap its Capture, InFront or Hooks.
type Evaluator interface {
	Evaluate(now time.Time)
}

// New builds the source. The configuration is copied; later changes to cfg do
// not reach the result.
func New(cfg Config) Evaluator {
	return &source{
		Samples:  cfg.Samples,
		Store:    cfg.Store,
		Readings: cfg.Readings,
		Events:   cfg.Events,
		Capture:  cfg.Capture,
		InFront:  cfg.InFront,
		Hooks:    cfg.Hooks,
		HostZone: cfg.HostZone,
	}
}

// source sets and clears the pane-menu condition. It is built only by New.
type source struct {
	Samples  Samples
	Store    Store
	Readings Readings
	Events   events.Emitter
	Capture  func(paneID string) (string, error)
	InFront  func(sess api.Session) bool
	Hooks    Hooks
	// HostZone is the zone a reset time is read in when the seat records none.
	// Nil means the host's own.
	HostZone *time.Location

	mu   sync.Mutex
	last map[string]time.Time
}

// due reports whether the seat may be captured at now and, if so, records it.
func (d *source) due(key string, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.last == nil {
		d.last = map[string]time.Time{}
	}
	if t, ok := d.last[key]; ok && now.Sub(t) < paneMenuInterval {
		return false
	}
	d.last[key] = now
	return true
}

// prune drops the cadence of sessions that no longer exist, so the map does not
// grow with every seat that ever ran.
func (d *source) prune(sessions []api.Session) {
	live := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		live[s.Key()] = true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for k := range d.last {
		if !live[k] {
			delete(d.last, k)
		}
	}
}

func (d *source) forget(key string) {
	d.mu.Lock()
	delete(d.last, key)
	d.mu.Unlock()
}

// Evaluate sets and clears the pane-menu condition on interactive seats. A seat
// whose condition another source set is left alone.
func (d *source) Evaluate(now time.Time) {
	sessions := d.Store.ListSessions()
	d.prune(sessions)
	if len(d.Samples.Menus) == 0 {
		// No sample: nothing can match, and nothing is captured to find out.
		// A condition already set still ends, below.
		d.clearOrphanedPaneMenus(sessions, now)
		return
	}
	for _, sess := range sessions {
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
		reading, state := d.Readings.Reading(api.AccountKeyOf(sess), now)
		if held {
			d.holdPaneMenu(sess, reading, state, now)
		} else {
			d.setPaneMenu(sess, state, now)
		}
	}
}

// clearOrphanedPaneMenus ends a pane-menu condition on a session that is gone,
// even when no sample is loaded.
func (d *source) clearOrphanedPaneMenus(sessions []api.Session, now time.Time) {
	for _, sess := range sessions {
		if sess.Limit != nil && sess.Limit.Source == api.LimitSourcePaneMenu && !sess.State.CountsAsAlive() {
			d.clearPaneMenu(sess, paneClearSessionEnded, now)
		}
	}
}

// setPaneMenu captures a seat that might be on the menu and sets the condition
// when the capture matches. Only a seat with no fresh reading, whose activity
// advisory reads stalled or unknown, is captured, and a working seat never is.
func (d *source) setPaneMenu(sess api.Session, readingState api.ReadingState, now time.Time) {
	if readingState == api.ReadingFresh {
		return
	}
	if sess.ActivityState != api.ActivityUnknown && sess.ActivityState != api.ActivityStalled {
		return
	}
	if !d.due(sess.Key(), now) {
		return
	}
	if !d.seen(sess) {
		return
	}
	capture, err := d.Capture(sess.PaneID)
	if err != nil {
		return
	}
	for _, sample := range d.Samples.Menus {
		res := limitmenu.Match(sample, capture)
		if !res.Matched {
			if d.Hooks.Seen != nil {
				d.Hooks.Seen(sess, sample, res)
			}
			continue
		}
		prov := d.paneMenuProvenance(sess, sample.Version, res.Span, now)
		var set bool
		_ = d.Store.UpdateSession(sess.Key(), func(live *api.Session) error {
			if live.Limit != nil {
				return nil // another source got there first
			}
			prov.ActivityAt = live.ContextAt
			live.Condition, live.Limit = api.ConditionLimited, prov
			set = true
			return nil
		})
		if set {
			events.Emit(d.Events, events.Event{
				Kind: events.KindSessionLimited, Severity: events.SeverityWarning,
				Workspace: sess.Workspace, Team: sess.Team, Role: sess.Role, Session: sess.Key(),
				Message: prov.Text(),
			})
			if d.Hooks.Matched != nil {
				d.Hooks.Matched(sess, sample, res, now)
			}
		}
		return
	}
}

// paneMenuProvenance builds the provenance for a matched capture, reading the
// reset span in the seat's zone when its environment records one.
func (d *source) paneMenuProvenance(sess api.Session, version, span string, now time.Time) *api.LimitProvenance {
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
func (d *source) holdPaneMenu(sess api.Session, reading api.AccountReading, state api.ReadingState, now time.Time) {
	if state == api.ReadingFresh && !hasFullWindow(reading, now) {
		d.clearPaneMenu(sess, paneClearFreshReading, now)
		return
	}
	if !sess.ContextAt.IsZero() && sess.ContextAt.After(sess.Limit.ActivityAt) {
		d.clearPaneMenu(sess, paneClearActivity, now)
		return
	}
	if !d.due(sess.Key(), now) {
		return
	}
	if !d.seen(sess) {
		return
	}
	capture, err := d.Capture(sess.PaneID)
	if err != nil {
		return // cannot see the pane: hold
	}
	for _, m := range d.Samples.Menus {
		if limitmenu.Match(m, capture).Matched {
			return
		}
	}
	for _, scr := range d.Samples.heldScreens() {
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
func (d *source) clearPaneMenu(sess api.Session, rule string, now time.Time) {
	var prov *api.LimitProvenance
	_ = d.Store.UpdateSession(sess.Key(), func(live *api.Session) error {
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
	// The cadence is forgotten only when the seat is gone. Forgetting at a
	// clear would let a still-stalled seat be captured again on the next tick.
	if rule == paneClearSessionEnded {
		d.forget(sess.Key())
	}
	base := events.Event{Workspace: sess.Workspace, Team: sess.Team, Role: sess.Role, Session: sess.Key()}
	un := base
	un.Kind, un.Severity = events.KindSessionUnlimited, events.SeverityInfo
	un.Message = fmt.Sprintf("limit cleared (%s): was %s", rule, prov.Text())
	events.Emit(d.Events, un)
	if d.Hooks.Cleared != nil {
		d.Hooks.Cleared(sess, rule)
	}
	if rule != paneClearSessionEnded {
		// Option A (design 9.4, ruled UL-R1): at the clear, say the seat can be
		// resumed. It carries nothing to type; a human or an awake seat acts.
		ev := base
		ev.Kind, ev.Severity = events.KindSeatResumeProposed, events.SeverityWarning
		ev.Message = fmt.Sprintf("%s is no longer limited (%s); it can be resumed", sess.Key(), rule)
		events.Emit(d.Events, ev)
	}

	switch {
	case rule == paneClearSessionEnded:
		// The seat is gone; there is no resume to report.
	case prov.ResetsAt.IsZero():
		ev := base
		ev.Kind, ev.Severity = events.KindLimitMenuResetUnknown, events.SeverityInfo
		ev.Message = fmt.Sprintf("cleared by %s; the menu's reset time could not be read (%s), so a resume before it cannot be judged", rule, prov.UntilNote)
		events.Emit(d.Events, ev)
	case now.Before(prov.ResetsAt):
		ev := base
		ev.Kind, ev.Severity = events.KindLimitMenuResumedBeforeReset, events.SeverityWarning
		ev.Message = fmt.Sprintf("cleared by %s with %s left before the reset the menu named (%s); this resume may have cost money or moved the seat to another account",
			rule, prov.ResetsAt.Sub(now).Round(time.Minute), prov.ResetsAt.UTC().Format(time.RFC3339))
		events.Emit(d.Events, ev)
	}
}

func (d *source) hostZone() *time.Location {
	if d.HostZone != nil {
		return d.HostZone
	}
	return time.Local
}

// seen reports whether the seat's pane may be captured: the harness marvel
// spawned there is the process in front (design 9.3 and the watchdog's gate,
// harness-state-watchdog-p1.md section 3). Without that answer nothing is read.
func (d *source) seen(sess api.Session) bool {
	return d.Capture != nil && d.InFront != nil && d.InFront(sess)
}
