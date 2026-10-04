// Package limitact is the limit action of design 9.4 and 9.9 (UL-7): what marvel
// does when the pane-menu source finds a seat on the usage-limit menu. The first
// implementation, Wait, is B's guarded selection of the second option.
//
// It is its own package so that the one key it can cause is fixed by
// construction. It is handed a Send function that takes a pane and no key, and a
// Capture function. It holds no tmux driver and cannot import the daemon, and a
// guard reads every file in the package. The key itself is a constant in the
// daemon's sender (internal/daemon/limitaction.go), tested through a fake driver.
//
// No samples, no key: it acts only where a menu sample and a P-UL7 measurement
// exist for the seat's harness version.
package limitact

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

const (
	// Polls and Interval make the 5 second confirmation window.
	Polls    = 10
	Interval = 500 * time.Millisecond
)

// Injector tags the daemon's own inject in the event ring.
const Injector = "transport=daemon injector=marvel:limit-wait"

// Deps is the whole of what the action reaches. Send types the one key the
// wiring chose into a pane; Capture reads its visible screen; Sleep waits.
type Deps struct {
	Samples panemenu.Samples
	Events  events.Emitter
	// Send delivers the selection key to the pane. It takes no key: the key is
	// fixed where the sender is built.
	Send    func(paneID string) error
	Capture func(paneID string) (string, error)
	Sleep   func(time.Duration)
}

// Action is option B: select the second option once per limit per seat.
type Action struct {
	deps Deps

	mu        sync.Mutex
	acted     map[string]bool              // a key went out for this limit
	announced map[string]bool              // unsampled or unselectable said for this limit
	refused   map[string]limitmenu.Refusal // last refusal said, until the pane stops showing it
	blocked   map[string]bool              // an unexpected screen: never answered again
}

// New returns the action. A nil Sleep is time.Sleep.
func New(d Deps) *Action {
	if d.Sleep == nil {
		d.Sleep = time.Sleep
	}
	return &Action{
		deps: d, acted: map[string]bool{}, announced: map[string]bool{},
		refused: map[string]limitmenu.Refusal{}, blocked: map[string]bool{},
	}
}

// Hooks are the callbacks the pane-menu source calls.
func (a *Action) Hooks() panemenu.Hooks {
	return panemenu.Hooks{Matched: a.Matched, Seen: a.Seen, Cleared: a.Cleared}
}

func (a *Action) emit(sess api.Session, kind events.Kind, sev events.Severity, msg string) {
	events.Emit(a.deps.Events, events.Event{
		Kind: kind, Severity: sev,
		Workspace: sess.Workspace, Team: sess.Team, Role: sess.Role, Session: sess.Key(),
		Message: msg,
	})
}

// Seen reports a menu block the matcher declined, once per reason until the pane
// stops showing it. A capture with no block at all is the ordinary case and says
// nothing.
func (a *Action) Seen(sess api.Session, sample limitmenu.Sample, res limitmenu.Result) {
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

// Cleared ends a limit: the next one may be answered once. A blocked seat stays
// blocked until its session ends.
func (a *Action) Cleared(sess api.Session, rule string) {
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
// sends and checks what the screen became.
func (a *Action) Matched(sess api.Session, sample limitmenu.Sample, _ limitmenu.Result, _ time.Time) {
	key := sess.Key()
	samples := a.deps.Samples

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

	if err := a.deps.Send(sess.PaneID); err != nil {
		a.emit(sess, events.KindLimitMenuUnconfirmed, events.SeverityWarning,
			fmt.Sprintf("the key could not be sent (sample %s): %v", sample.Version, err))
		return
	}
	a.emit(sess, events.KindSessionInjected, events.SeverityInfo,
		fmt.Sprintf("inject key (selection) enter=false %s", Injector))
	a.confirm(sess, sample, samples)
}

type screenKind int

const (
	screenNone screenKind = iota - 1 // no capture could be read
	screenOther
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
func (a *Action) confirm(sess api.Session, sample limitmenu.Sample, samples panemenu.Samples) {
	last, lastKind := "", screenNone
	for i := 0; i < Polls; i++ {
		a.deps.Sleep(Interval)
		capture, err := a.deps.Capture(sess.PaneID)
		if err != nil {
			lastKind = screenNone
			continue
		}
		last, lastKind = capture, classifyScreen(capture, sample, samples)
		if lastKind == screenAnswered {
			a.emit(sess, events.KindLimitMenuAnswered, events.SeverityInfo,
				fmt.Sprintf("limit menu answered (sample %s): the second option was taken", sample.Version))
			return
		}
		if lastKind == screenOptionOne {
			break
		}
	}
	switch lastKind {
	case screenMenu, screenNone:
		a.emit(sess, events.KindLimitMenuUnconfirmed, events.SeverityWarning,
			fmt.Sprintf("limit menu: the key was sent but the screen was not seen to change within %s (sample %s); nothing more will be sent", Polls*Interval, sample.Version))
	default:
		a.mu.Lock()
		a.blocked[sess.Key()] = true
		a.mu.Unlock()
		// The event ring has no error severity; warning is what the ring-to-NATS
		// tap carries, so an unexpected screen reaches a human. No screen text is
		// carried: a login or device-code screen is exactly what could land here.
		a.emit(sess, events.KindLimitMenuUnexpected, events.SeverityWarning,
			fmt.Sprintf("UNEXPECTED screen after the key (sample %s, %s); this seat is not answered again until an operator clears it. No screen text is carried; capture the pane to see it",
				sample.Version, shape(last, lastKind == screenOptionOne)))
	}
}

// shape describes a capture by its size and, when it matched a known screen,
// which: counts only, never a row.
func shape(capture string, optionOne bool) string {
	rows, nonBlank := 0, 0
	for _, l := range strings.Split(capture, "\n") {
		rows++
		if strings.TrimSpace(l) != "" {
			nonBlank++
		}
	}
	known := "no known screen"
	if optionOne {
		known = "the option-1 screen"
	}
	return fmt.Sprintf("%s; %d rows, %d non-blank, %d bytes", known, rows, nonBlank, len(capture))
}
