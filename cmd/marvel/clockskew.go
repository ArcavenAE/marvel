package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// clockTolerance is how far past the client's now a daemon stamp may sit
// before it reads as a future stamp: the round trip of the call that fetched
// it. Zero until a one-shot read measures one, and left zero in watch mode,
// where a fetch runs off the render goroutine. Display only: nothing gates on
// it (aae-orc SOUL section 8).
var clockTolerance time.Duration

// stampSkew is the reason a cell reads ?: which stamp, the state future, and
// how far it sits past the client's now. Skew is a Go duration string.
type stampSkew struct {
	Field string `json:"field"`
	State string `json:"state"`
	Skew  string `json:"skew"`
}

// clockSkewKey is the JSON key a session object gains when it holds a future
// stamp. It is omitted otherwise.
const clockSkewKey = "clock_skew"

// futureSkew reports how far stamp sits past now, and whether that is more
// than tol. A zero stamp is never in the future.
func futureSkew(stamp, now time.Time, tol time.Duration) (time.Duration, bool) {
	if stamp.IsZero() || !stamp.After(now.Add(tol)) {
		return 0, false
	}
	return stamp.Sub(now), true
}

// skewNotes lists a session's future stamps, under the JSON key each one has
// in the session object. These are the stamps the table cells read.
func skewNotes(s api.Session, now time.Time, tol time.Duration) []stampSkew {
	var notes []stampSkew
	for _, st := range []struct {
		field string
		at    time.Time
	}{
		{"ContextAt", s.ContextAt},
		{"reading_changed_at", s.ReadingChangedAt},
		{"totals_changed_at", s.TotalsChangedAt},
	} {
		if skew, ok := futureSkew(st.at, now, tol); ok {
			notes = append(notes, stampSkew{Field: st.field, State: "future", Skew: skew.String()})
		}
	}
	return notes
}

// sessionWithSkew is a session plus its clock_skew list, for -o output. The
// session's own fields keep their order and values.
type sessionWithSkew struct {
	api.Session
	ClockSkew []stampSkew `json:"clock_skew,omitempty"`
}

// sessionsWithSkew wraps each session with its future-stamp notes.
func sessionsWithSkew(sessions []api.Session, now time.Time, tol time.Duration) []sessionWithSkew {
	out := make([]sessionWithSkew, len(sessions))
	for i, s := range sessions {
		out[i] = sessionWithSkew{Session: s, ClockSkew: skewNotes(s, now, tol)}
	}
	return out
}

// withClockSkew adds a clock_skew list to a session object, or to each session
// of a list, for every stamp later than now plus tol. It rewrites no stamp.
// Anything that is not a session or a list of sessions comes back unchanged.
func withClockSkew(raw []byte, now time.Time, tol time.Duration) ([]byte, error) {
	var list []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &list); err == nil {
		for _, obj := range list {
			if err := addSkew(obj, now, tol); err != nil {
				return nil, err
			}
		}
		return json.Marshal(list)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw, nil
	}
	if err := addSkew(obj, now, tol); err != nil {
		return nil, err
	}
	return json.Marshal(obj)
}

func addSkew(obj map[string]json.RawMessage, now time.Time, tol time.Duration) error {
	b, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	var s api.Session
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("read session for clock skew: %w", err)
	}
	notes := skewNotes(s, now, tol)
	if len(notes) == 0 {
		return nil
	}
	nb, err := json.Marshal(notes)
	if err != nil {
		return err
	}
	obj[clockSkewKey] = nb
	return nil
}

// measureRoundTrip runs call and returns how long it took on lastActiveClock.
func measureRoundTrip(call func() error) (time.Duration, error) {
	start := lastActiveClock()
	err := call()
	return lastActiveClock().Sub(start), err
}

// sendTimed is send, and it sets clockTolerance to the round trip it measured,
// so the cells that follow judge a daemon stamp against the client's clock plus
// that trip. It is for one-shot reads only; watch mode never calls it.
func sendTimed(req daemonrpc.Request) (*daemonrpc.Response, error) {
	var resp *daemonrpc.Response
	rtt, err := measureRoundTrip(func() error {
		var e error
		resp, e = send(req)
		return e
	})
	if err == nil {
		clockTolerance = rtt
	}
	return resp, err
}
