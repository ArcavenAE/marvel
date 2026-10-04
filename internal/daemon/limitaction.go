package daemon

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/limitmenu"
	"github.com/arcavenae/marvel/internal/panemenu"
)

// The limit action (design 9.4 and 9.9, UL-7). A limit action is what marvel
// does when the pane-menu source sees a seat on the limit menu. The interface
// is pluggable: a /login migration of the running session to another backend is
// a named future action and is not designed here. The first implementation is
// B's guarded selection, selectWait.
//
// It acts only where samples exist: a menu sample and a P-UL7 measurement for
// the seat's harness version. No samples, no key. The pane-menu source itself
// is isolated (internal/panemenu) and sends nothing; this file is the one place
// a key can go out, it sends one constant, and a guard test reads it.

// limitKey is the only key a limit action ever sends: the digit that selects
// the menu's second option, "Wait here, then continue automatically". Never
// Enter, an arrow or Escape, and never the digit for "Switch to usage credits".
const limitKey = "2"

// limitInjector tags the daemon's own inject in the event ring.
const limitInjector = "transport=daemon injector=marvel:limit-wait"

const (
	// confirmPolls and confirmInterval make the 5 second confirmation window.
	confirmPolls    = 10
	confirmInterval = 500 * time.Millisecond
	// excerptRows bounds the capture carried on limit-menu.unexpected.
	excerptRows  = 6
	excerptBytes = 120
)

// limitAction is what happens when a seat is found on the limit menu.
type limitAction interface {
	Name() string
	// Matched is called once per limit, after the condition was set.
	Matched(sess api.Session, sample limitmenu.Sample, res limitmenu.Result, now time.Time)
	// Seen is called for a capture that did not match a sample.
	Seen(sess api.Session, sample limitmenu.Sample, res limitmenu.Result)
	// Cleared is called when the seat's pane-menu condition ended.
	Cleared(sess api.Session, rule string)
}

// selectWait is option B: select the second option once per limit per seat.
type selectWait struct {
	d *Daemon
	// send, capture and sleep default to the tmux driver and the clock; tests
	// replace them.
	send    func(paneID, key string) error
	capture func(paneID string) (string, error)
	sleep   func(time.Duration)

	mu        sync.Mutex
	acted     map[string]bool              // a key went out for this limit
	announced map[string]bool              // unsampled or unselectable said for this limit
	refused   map[string]limitmenu.Refusal // last refusal said, until the pane stops showing it
	blocked   map[string]bool              // an unexpected screen: never answered again
}

func newSelectWait(d *Daemon) *selectWait {
	return &selectWait{
		d:         d,
		send:      func(paneID, key string) error { return d.driver.SendKeys(paneID, key, false, false) },
		capture:   d.driver.CapturePaneJoined,
		sleep:     time.Sleep,
		acted:     map[string]bool{},
		announced: map[string]bool{},
		refused:   map[string]limitmenu.Refusal{},
		blocked:   map[string]bool{},
	}
}

func (a *selectWait) Name() string { return "limit-wait" }

func (a *selectWait) emit(sess api.Session, kind events.Kind, sev events.Severity, msg string) {
	events.Emit(a.d.events, events.Event{
		Kind: kind, Severity: sev,
		Workspace: sess.Workspace, Team: sess.Team, Role: sess.Role, Session: sess.Key(),
		Message: msg,
	})
}

// Seen reports a menu block the matcher declined, once per reason until the pane
// stops showing it. A capture with no block at all is the ordinary case and says
// nothing.
func (a *selectWait) Seen(sess api.Session, sample limitmenu.Sample, res limitmenu.Result) {
	key := sess.Key()
	switch res.Refusal {
	case limitmenu.RefusalTrailingRows, limitmenu.RefusalCursorCount, limitmenu.RefusalCursorRow:
	default:
		a.mu.Lock()
		delete(a.refused, key)
		a.mu.Unlock()
		return
	}
	a.mu.Lock()
	again := a.refused[key] == res.Refusal
	a.refused[key] = res.Refusal
	a.mu.Unlock()
	if again {
		return
	}
	a.emit(sess, events.KindLimitMenuRefused, events.SeverityWarning,
		fmt.Sprintf("limit menu not answered: %s (sample %s); nothing was sent", res.Refusal, sample.Version))
}

func (a *selectWait) Cleared(sess api.Session, rule string) {
	key := sess.Key()
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.acted, key)
	delete(a.announced, key)
	delete(a.refused, key)
	if rule == "session-ended" {
		delete(a.blocked, key)
	}
}

// Matched decides, once per limit, whether a key may go out, and if it does,
// sends the digit and checks what the screen became.
func (a *selectWait) Matched(sess api.Session, sample limitmenu.Sample, _ limitmenu.Result, _ time.Time) {
	key := sess.Key()
	samples := a.d.limitMenu

	a.mu.Lock()
	if a.acted[key] || a.announced[key] {
		a.mu.Unlock()
		return
	}
	if a.blocked[key] {
		a.announced[key] = true
		a.mu.Unlock()
		a.emit(sess, events.KindLimitMenuRefused, events.SeverityWarning,
			fmt.Sprintf("limit menu not answered: this seat met an unexpected screen after the key and is not answered again (sample %s); nothing was sent", sample.Version))
		return
	}
	var measured, selects int
	for _, sel := range samples.Selections {
		if sel.Version != sample.Version {
			continue
		}
		measured++
		if sel.SelectsOptionTwo() {
			selects++
		}
	}
	switch {
	case measured == 0:
		a.announced[key] = true
		a.mu.Unlock()
		a.emit(sess, events.KindLimitMenuUnsampled, events.SeverityWarning,
			fmt.Sprintf("limit menu matched (sample %s) but no post-selection sample exists for this harness version; nothing was sent", sample.Version))
		return
	case selects == 0:
		a.announced[key] = true
		a.mu.Unlock()
		a.emit(sess, events.KindLimitMenuUnselectable, events.SeverityWarning,
			fmt.Sprintf("limit menu matched (sample %s) but the measurements show the digit does not select option 2; nothing was sent", sample.Version))
		return
	}
	a.acted[key] = true // one key per limit per seat, whatever happens next
	a.mu.Unlock()

	if err := a.send(sess.PaneID, limitKey); err != nil {
		a.emit(sess, events.KindLimitMenuUnconfirmed, events.SeverityWarning,
			fmt.Sprintf("the key could not be sent (sample %s): %v", sample.Version, err))
		return
	}
	recordInject(a.d.events, sess, injectParams{Text: limitKey}, limitInjector)
	a.confirm(sess, sample, samples)
}

type screenKind int

const (
	screenOther screenKind = iota
	screenMenu
	screenAnswered
	screenOptionOne
)

func classifyScreen(capture string, sample limitmenu.Sample, s panemenu.Samples) screenKind {
	for _, scr := range s.OptionOne {
		if limitmenu.MatchScreen(scr, capture).Matched {
			return screenOptionOne
		}
	}
	for _, sel := range s.Selections {
		if sel.Version == sample.Version && sel.SelectsOptionTwo() && limitmenu.MatchScreen(sel.After, capture).Matched {
			return screenAnswered
		}
	}
	if res := limitmenu.Match(sample, capture); res.Matched || (res.Refusal != limitmenu.RefusalNoBlock && res.Refusal != limitmenu.RefusalNoSample) {
		return screenMenu
	}
	return screenOther
}

// confirm re-captures for up to five seconds. Answered ends it; the option-1
// screen is unexpected at once; the menu still showing at the end is
// unconfirmed; any other screen at the end is unexpected. Nothing more is sent.
func (a *selectWait) confirm(sess api.Session, sample limitmenu.Sample, samples panemenu.Samples) {
	last, lastKind := "", screenKind(-1)
	for i := 0; i < confirmPolls; i++ {
		a.sleep(confirmInterval)
		capture, err := a.capture(sess.PaneID)
		if err != nil {
			lastKind = -1
			continue
		}
		last, lastKind = capture, classifyScreen(capture, sample, samples)
		if lastKind == screenAnswered {
			a.emit(sess, events.KindLimitMenuAnswered, events.SeverityInfo,
				fmt.Sprintf("limit menu answered with %s (sample %s): the second option was taken", limitKey, sample.Version))
			return
		}
		if lastKind == screenOptionOne {
			break
		}
	}
	switch lastKind {
	case screenMenu, -1:
		a.emit(sess, events.KindLimitMenuUnconfirmed, events.SeverityWarning,
			fmt.Sprintf("limit menu: the key was sent but the screen was not seen to change within %s (sample %s); nothing more will be sent", confirmPolls*confirmInterval, sample.Version))
	default:
		a.mu.Lock()
		a.blocked[sess.Key()] = true
		a.mu.Unlock()
		// The event ring has no error severity; warning is what the ring-to-NATS
		// tap carries, so an unexpected screen reaches a human.
		a.emit(sess, events.KindLimitMenuUnexpected, events.SeverityWarning,
			fmt.Sprintf("UNEXPECTED screen after the key (sample %s); this seat is not answered again until an operator clears it. Last rows: %s", sample.Version, excerpt(last)))
	}
}

// excerpt is the last few non-blank rows of a capture, each bounded.
func excerpt(capture string) string {
	var rows []string
	for _, l := range strings.Split(capture, "\n") {
		if l = strings.TrimRight(l, " \r"); l != "" {
			rows = append(rows, l)
		}
	}
	if len(rows) > excerptRows {
		rows = rows[len(rows)-excerptRows:]
	}
	for i, r := range rows {
		if len(r) > excerptBytes {
			rows[i] = r[:excerptBytes] + "..."
		}
	}
	return strings.Join(rows, " | ")
}
