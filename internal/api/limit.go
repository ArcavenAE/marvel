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
}

// Text renders the provenance as one line for a person.
func (p LimitProvenance) Text() string {
	return fmt.Sprintf("limited until %s (%s window at %.0f%%, account %s, reading from %s at %s)",
		p.ResetsAt.UTC().Format(time.RFC3339), p.Window, p.UsedPercent, p.Account, p.ReportedBy, p.ReadingAt.UTC().Format(time.RFC3339))
}

// LimitChange is what EvaluateLimit did to a condition.
type LimitChange string

const (
	LimitUnchanged LimitChange = ""
	LimitSet       LimitChange = "limited"
	LimitCleared   LimitChange = "unlimited"
)

// Reasons EvaluateLimit gives for a clear.
const (
	LimitClearResetReached = "reset-reached"
	LimitClearBelowLimit   = "newer-reading-below-limit"
)

// EvaluateLimit decides a session's reading-sourced condition from its
// account's reading. cur is the stored provenance, nil if not limited.
//
// A session is limited when a fresh reading has a window at 100 percent or
// more whose reset is still ahead. It stays limited until that reset, or until
// a newer fresh reading puts the same window below 100. A stale reading never
// sets a condition and never clears one; a stored condition ends by its own
// reset time, so it needs no reading and survives a daemon restart. A
// condition another source set (the pane menu) is left alone.
func EvaluateLimit(cur *LimitProvenance, key AccountKey, reading AccountReading, state ReadingState, now time.Time) (next *LimitProvenance, change LimitChange, reason string) {
	if cur != nil {
		if cur.Source != LimitSourceReading {
			return cur, LimitUnchanged, ""
		}
		if !now.Before(cur.ResetsAt) {
			return nil, LimitCleared, LimitClearResetReached
		}
		if state == ReadingFresh && reading.At.After(cur.ReadingAt) {
			for _, w := range reading.Windows {
				if w.Name == cur.Window && w.UsedPercent != nil && *w.UsedPercent < 100 {
					return nil, LimitCleared, LimitClearBelowLimit
				}
			}
		}
		return cur, LimitUnchanged, ""
	}
	if state != ReadingFresh {
		return nil, LimitUnchanged, ""
	}
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
	if bind == nil {
		return nil, LimitUnchanged, ""
	}
	return &LimitProvenance{
		Source: LimitSourceReading, Window: bind.Name, UsedPercent: *bind.UsedPercent,
		ResetsAt: bind.ResetsAt, ReportedBy: reading.Session, ReadingAt: reading.At, Account: key.String(),
	}, LimitSet, ""
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
