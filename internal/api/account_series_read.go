package api

import (
	"sort"
	"strings"
	"time"
)

// NewestReading is the newest reading of one account as the series holds it:
// the windows whose newest sample is within ReadingMaxAge of the account's
// newest sample, the session that took the newest, and its time.
type NewestReading struct {
	Key     AccountKey
	Windows []AccountWindow
	Session string
	At      time.Time
}

// Newest rebuilds each account's newest reading from the series, in a stable
// order. A reading is reported window by window and an unchanged window is
// repeated only about once a minute, so the windows of one reading are the ones
// whose newest sample sits close to the account's newest. A window older than
// ReadingMaxAge before that is left out, not carried as if it were current.
func (s *AccountSeries) Newest() []NewestReading {
	s.mu.Lock()
	defer s.mu.Unlock()
	newest := map[AccountKey]AccountSample{}
	for sk, cur := range s.m {
		if len(cur) == 0 {
			continue
		}
		if last := cur[len(cur)-1]; last.At.After(newest[sk.account].At) {
			newest[sk.account] = last
		}
	}
	var out []NewestReading
	for key, head := range newest {
		r := NewestReading{Key: key, Session: head.Session, At: head.At}
		for sk, cur := range s.m {
			if sk.account != key || len(cur) == 0 {
				continue
			}
			last := cur[len(cur)-1]
			if head.At.Sub(last.At) > ReadingMaxAge {
				continue
			}
			used := last.UsedPercent
			r.Windows = append(r.Windows, AccountWindow{Name: sk.window, UsedPercent: &used, ResetsAt: last.ResetsAt})
		}
		sort.Slice(r.Windows, func(i, j int) bool { return r.Windows[i].Name < r.Windows[j].Name })
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key.String() < out[j].Key.String() })
	return out
}

// Recent returns every sample with At >= from, across all accounts and windows,
// ordered by account, window and time. A zero from leaves the start open. A
// non-empty account keeps only the accounts whose key text contains it.
func (s *AccountSeries) Recent(account string, from time.Time) []AccountSample {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []AccountSample
	for sk, cur := range s.m {
		if account != "" && !strings.Contains(sk.account.String(), account) {
			continue
		}
		for _, smp := range cur {
			if !from.IsZero() && smp.At.Before(from) {
				continue
			}
			out = append(out, smp)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Account != b.Account {
			return a.Account < b.Account
		}
		if a.Window != b.Window {
			return a.Window < b.Window
		}
		return a.At.Before(b.At)
	})
	return out
}
