package api

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// DefaultSeriesRetention is how long an account-window reading is kept. It is
// one seven_day window plus a day, so a run that ended near the end of a
// window can still be read against its start. It is a default, not a ruling.
const DefaultSeriesRetention = 8 * 24 * time.Hour

// SeriesMaxSamples bounds one window's series. A statusline ticks often, and
// the memory held must not grow with the tick rate.
const SeriesMaxSamples = 10000

// seriesRepeatGap is how long an unchanged reading is treated as the same
// reading: a statusline repeats the figure every tick.
const seriesRepeatGap = time.Minute

// AccountSample is one reading of one window of one account, stamped with the
// cluster that took it and the time it was observed.
type AccountSample struct {
	Cluster     string    `json:"cluster"`
	Account     string    `json:"account"`
	Window      string    `json:"window"`
	UsedPercent float64   `json:"used_percent"`
	ResetsAt    time.Time `json:"resets_at,omitempty"`
	At          time.Time `json:"at"`
	Session     string    `json:"session,omitempty"`
}

// RunWindowDelta is what a run did to one window: the used percentage at its
// start, at its end, and the difference. A pointer that is nil is unknown, and
// unknown is never zero; Note says why.
type RunWindowDelta struct {
	Cluster      string     `json:"cluster"`
	Account      string     `json:"account"`
	Window       string     `json:"window"`
	Start        time.Time  `json:"start"`
	End          time.Time  `json:"end"`
	StartPercent *float64   `json:"start_percent,omitempty"`
	EndPercent   *float64   `json:"end_percent,omitempty"`
	Delta        *float64   `json:"delta,omitempty"`
	StartReading *time.Time `json:"start_reading_at,omitempty"`
	EndReading   *time.Time `json:"end_reading_at,omitempty"`
	// Rollover is true when the window reset between the two readings: the
	// figure restarted, so the difference is not a burn and is left unknown.
	Rollover bool   `json:"rollover,omitempty"`
	Note     string `json:"note,omitempty"`
}

type seriesSlot struct {
	account AccountKey
	window  string
}

// AccountSeries keeps time-stamped readings per account and window, in memory.
// It is fed by the same account.limits readings AccountReadings receives, and
// asks nothing of the harness. A daemon restart empties it.
type AccountSeries struct {
	mu        sync.Mutex
	cluster   string
	retention time.Duration
	m         map[seriesSlot][]AccountSample
}

// NewAccountSeries returns an empty series. cluster stamps every sample; a
// retention of zero or less means DefaultSeriesRetention.
func NewAccountSeries(cluster string, retention time.Duration) *AccountSeries {
	if retention <= 0 {
		retention = DefaultSeriesRetention
	}
	return &AccountSeries{cluster: cluster, retention: retention, m: map[seriesSlot][]AccountSample{}}
}

// SetCluster sets the name that stamps samples taken from now on. The daemon
// learns its cluster's name after it is built.
func (s *AccountSeries) SetCluster(name string) {
	s.mu.Lock()
	s.cluster = name
	s.mu.Unlock()
}

// Append records the windows of one reading observed at at. A window with no
// usable percentage is not recorded. A reading older than the retention before
// the newest one held is dropped.
func (s *AccountSeries) Append(key AccountKey, windows []AccountWindow, session string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range windows {
		if w.UsedPercent == nil || !usablePercent(*w.UsedPercent) {
			continue
		}
		sk := seriesSlot{key, w.Name}
		cur := s.m[sk]
		smp := AccountSample{
			Cluster: s.cluster, Account: key.String(), Window: w.Name,
			UsedPercent: *w.UsedPercent, ResetsAt: w.ResetsAt, At: at.UTC(), Session: session,
		}
		i := sort.Search(len(cur), func(i int) bool { return cur[i].At.After(smp.At) })
		if i > 0 {
			prev := cur[i-1]
			if prev.UsedPercent == smp.UsedPercent && prev.ResetsAt.Equal(smp.ResetsAt) && smp.At.Sub(prev.At) < seriesRepeatGap {
				continue
			}
		}
		cur = append(cur, AccountSample{})
		copy(cur[i+1:], cur[i:])
		cur[i] = smp
		cutoff := cur[len(cur)-1].At.Add(-s.retention)
		drop := sort.Search(len(cur), func(i int) bool { return !cur[i].At.Before(cutoff) })
		if over := len(cur) - drop - SeriesMaxSamples; over > 0 {
			drop += over
		}
		s.m[sk] = append([]AccountSample(nil), cur[drop:]...)
	}
}

func usablePercent(p float64) bool { return p >= 0 && p <= 1e6 }

// Samples returns the readings of one window with from <= At <= to, oldest
// first. A zero from or to leaves that end open.
func (s *AccountSeries) Samples(key AccountKey, window string, from, to time.Time) []AccountSample {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []AccountSample
	for _, smp := range s.m[seriesSlot{key, window}] {
		if (!from.IsZero() && smp.At.Before(from)) || (!to.IsZero() && smp.At.After(to)) {
			continue
		}
		out = append(out, smp)
	}
	return out
}

// at returns the newest sample at or before t that is no older than
// ReadingMaxAge at t.
func (s *AccountSeries) at(key AccountKey, window string, t time.Time) (AccountSample, bool) {
	cur := s.m[seriesSlot{key, window}]
	i := sort.Search(len(cur), func(i int) bool { return cur[i].At.After(t) })
	if i == 0 {
		return AccountSample{}, false
	}
	smp := cur[i-1]
	if t.Sub(smp.At) > ReadingMaxAge {
		return AccountSample{}, false
	}
	return smp, true
}

// Delta reports a window's used percentage at start and at end of a run, and
// the difference. Each end uses the newest reading at or before it that is
// still within ReadingMaxAge; with none, that end is unknown. The difference is
// unknown when either end is, when end precedes start, and when the window
// reset between the two readings, since the figure then restarted.
func (s *AccountSeries) Delta(key AccountKey, window string, start, end time.Time) RunWindowDelta {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := RunWindowDelta{Cluster: s.cluster, Account: key.String(), Window: window, Start: start.UTC(), End: end.UTC()}
	if end.Before(start) {
		d.Note = "run end precedes its start"
		return d
	}
	a, aok := s.at(key, window, start)
	b, bok := s.at(key, window, end)
	if aok {
		p, at := a.UsedPercent, a.At
		d.StartPercent, d.StartReading = &p, &at
	}
	if bok {
		p, at := b.UsedPercent, b.At
		d.EndPercent, d.EndReading = &p, &at
	}
	switch {
	case !aok && !bok:
		d.Note = fmt.Sprintf("no reading within %s of the start or the end", ReadingMaxAge)
	case !aok:
		d.Note = fmt.Sprintf("no reading within %s of the start", ReadingMaxAge)
	case !bok:
		d.Note = fmt.Sprintf("no reading within %s of the end", ReadingMaxAge)
	case !a.ResetsAt.Equal(b.ResetsAt):
		d.Rollover = true
		d.Note = "the window reset between the two readings"
	case b.UsedPercent < a.UsedPercent:
		d.Note = "the used percentage fell inside one window; no burn is reported"
	default:
		v := b.UsedPercent - a.UsedPercent
		d.Delta = &v
	}
	return d
}

// AccountSeriesRequest is the account.series RPC body. The account is the one
// the named session spends against. A zero From or To leaves that end open.
type AccountSeriesRequest struct {
	Session string    `json:"session"`
	Window  string    `json:"window"`
	From    time.Time `json:"from,omitempty"`
	To      time.Time `json:"to,omitempty"`
}

// AccountDeltaRequest is the account.delta RPC body: a run's start and end
// instants, and the session whose account the run spent against.
type AccountDeltaRequest struct {
	Session string    `json:"session"`
	Window  string    `json:"window"`
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
}
