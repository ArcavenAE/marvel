package api

import (
	"fmt"
	"time"
)

// SessionCondition is a restart-neutral advisory about a session.
type SessionCondition string

// ConditionLimited means the session's account is at a rate limit.
const ConditionLimited SessionCondition = "limited"

// LimitSourceReading marks a condition set from an account reading.
const LimitSourceReading = "reading"

// LimitSourcePaneMenu marks a condition set because a seat's pane showed the
// usage-limit menu. It is the one place pane text sets a condition (design 9.3,
// UL-R2), and it never sends a key.
const LimitSourcePaneMenu = "pane-menu"

// LimitProvenance says why a session is limited. It is stored with the
// condition so the condition can clear at ResetsAt after a restart.
type LimitProvenance struct {
	Source      string    `json:"source"`
	Window      string    `json:"window,omitempty"`
	UsedPercent float64   `json:"used_percent,omitempty"`
	ResetsAt    time.Time `json:"resets_at,omitempty"`
	ReportedBy  string    `json:"reported_by,omitempty"`
	ReadingAt   time.Time `json:"reading_at,omitempty"`
	Account     string    `json:"account,omitempty"`

	// The fields below are set only by the pane-menu source. SampleVersion is
	// the harness version of the sample the capture matched, CapturedAt when it
	// was captured, Span the reset text as shown, and Zone the zone that text
	// was read in. ResetsAt holds the parsed until, and is zero when the span
	// could not be read (UntilNote says why). ActivityAt is the seat's activity
	// signal when the condition was set; an advance past it means the seat is
	// working again.
	SampleVersion string    `json:"sample_version,omitempty"`
	CapturedAt    time.Time `json:"captured_at,omitempty"`
	Span          string    `json:"span,omitempty"`
	Zone          string    `json:"zone,omitempty"`
	UntilNote     string    `json:"until_note,omitempty"`
	ActivityAt    time.Time `json:"activity_at,omitempty"`
}

// Text renders the provenance as one line for a person.
func (p LimitProvenance) Text() string {
	if p.Source == LimitSourcePaneMenu {
		until := "an unknown time (" + p.UntilNote + ")"
		if !p.ResetsAt.IsZero() {
			until = fmt.Sprintf("%s (read in %s from %q)", p.ResetsAt.UTC().Format(time.RFC3339), p.Zone, p.Span)
		}
		return fmt.Sprintf("limited until %s (limit menu on the pane, sample %s, captured at %s)",
			until, p.SampleVersion, p.CapturedAt.UTC().Format(time.RFC3339))
	}
	return fmt.Sprintf("limited until %s (%s window at %.0f%%, account %s, reading from %s at %s)",
		p.ResetsAt.UTC().Format(time.RFC3339), p.Window, p.UsedPercent, p.Account, p.ReportedBy, p.ReadingAt.UTC().Format(time.RFC3339))
}

// LimitChange is what EvaluateLimit did to a condition.
type LimitChange string

const (
	LimitUnchanged LimitChange = ""
	LimitSet       LimitChange = "limited"
	LimitCleared   LimitChange = "unlimited"
	// LimitRebound means the session stays limited but the window that binds
	// it changed. It is silent: no event, because the transition count is
	// unchanged, but the new provenance must be stored so the condition ends
	// when the window that still binds it resets.
	LimitRebound LimitChange = "rebound"
)

// Reasons EvaluateLimit gives for a clear.
const (
	LimitClearResetReached = "reset-reached"
	LimitClearBelowLimit   = "newer-reading-below-limit"
)

// EvaluateLimit decides a session's reading-sourced condition from its
// account's reading. cur is the stored provenance, nil if not limited.
//
// A session is limited when a reading, fresh or stale, has a window at 100
// percent or more whose reset is still ahead. It is bound to the full window that resets
// last. It stays limited until that reset, or until a newer fresh reading
// leaves no window at 100 with a reset ahead. If a newer reading puts the bound
// window below 100 while another window is still full, the session stays
// limited and is rebound to the one that still binds it, with no clear and no
// new set: a false unlimited followed by limited would break once per
// transition. A stale reading may set a condition (a full window cannot
// have reset before its reset time) but never clears one; a stored condition ends by its own reset time, so it needs no reading and
// survives a daemon restart. A condition another source set (the pane menu) is
// left alone.
func EvaluateLimit(cur *LimitProvenance, key AccountKey, reading AccountReading, state ReadingState, now time.Time) (next *LimitProvenance, change LimitChange, reason string) {
	if cur != nil {
		if cur.Source != LimitSourceReading {
			return cur, LimitUnchanged, ""
		}
		if !now.Before(cur.ResetsAt) {
			return nil, LimitCleared, LimitClearResetReached
		}
		if state != ReadingFresh || !reading.At.After(cur.ReadingAt) {
			return cur, LimitUnchanged, ""
		}
		if bound := bindingWindow(reading, now); bound != nil {
			if bound.Name == cur.Window && bound.ResetsAt.Equal(cur.ResetsAt) {
				return cur, LimitUnchanged, ""
			}
			return provenanceFor(key, reading, bound), LimitRebound, ""
		}
		for _, w := range reading.Windows {
			if w.Name == cur.Window && w.UsedPercent != nil && *w.UsedPercent < 100 {
				return nil, LimitCleared, LimitClearBelowLimit
			}
		}
		return cur, LimitUnchanged, ""
	}
	if state == ReadingNone {
		return nil, LimitUnchanged, ""
	}
	bind := bindingWindow(reading, now)
	if bind == nil {
		return nil, LimitUnchanged, ""
	}
	return provenanceFor(key, reading, bind), LimitSet, ""
}

// bindingWindow is the full window (100 percent or more) with a reset still
// ahead that resets last, or nil if there is none.
func bindingWindow(reading AccountReading, now time.Time) *AccountWindow {
	var bind *AccountWindow
	for i := range reading.Windows {
		w := &reading.Windows[i]
		if w.UsedPercent == nil || *w.UsedPercent < 100 || !w.ResetsAt.After(now) {
			continue
		}
		if bind == nil || w.ResetsAt.After(bind.ResetsAt) {
			bind = w
		}
	}
	return bind
}

func provenanceFor(key AccountKey, reading AccountReading, w *AccountWindow) *LimitProvenance {
	return &LimitProvenance{
		Source: LimitSourceReading, Window: w.Name, UsedPercent: *w.UsedPercent,
		ResetsAt: w.ResetsAt, ReportedBy: reading.Session, ReadingAt: reading.At, Account: key.String(),
	}
}

// LimitReadingText renders a reading state for describe: "fresh 87% (seven_day)",
// "stale (last 2026-10-03T22:10Z)" or "none". For a fresh reading it names the
// fullest window.
func LimitReadingText(reading AccountReading, state ReadingState) string {
	switch state {
	case ReadingFresh:
		var top *AccountWindow
		for i := range reading.Windows {
			w := &reading.Windows[i]
			if w.UsedPercent != nil && (top == nil || *w.UsedPercent > *top.UsedPercent) {
				top = w
			}
		}
		if top == nil {
			return "none"
		}
		return fmt.Sprintf("fresh %.0f%% (%s)", *top.UsedPercent, top.Name)
	case ReadingStale:
		return "stale (last " + reading.At.UTC().Format(time.RFC3339) + ")"
	default:
		return "none"
	}
}
