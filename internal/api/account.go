package api

import (
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// DimAccountWindow is the dimension of a `get budgets` row that reports an
// account's rate-limit window rather than a team clause. It is deliberately
// not in the budget registry: a manifest cannot declare it, and nothing is
// enforced against it. See docs/design/usage-limit-pause.md section 2.
const DimAccountWindow Dimension = "account_window"

// ReadingMaxAge is how old an account reading may be and still show as a
// number. Older than this it shows as stale.
const ReadingMaxAge = 15 * time.Minute

// ReadingState says how much a view may trust an account's reading.
type ReadingState string

const (
	// ReadingFresh: a usable reading arrived within ReadingMaxAge.
	ReadingFresh ReadingState = "fresh"
	// ReadingStale: the newest usable reading is older than ReadingMaxAge.
	ReadingStale ReadingState = "stale"
	// ReadingNone: no usable reading was ever received for the account.
	ReadingNone ReadingState = "none"
)

// AccountKey identifies the account a session spends against, derived from
// what marvel already records. It is derived, not read from the credential,
// which marvel does not hold (ADR-009). Two sessions with the same key are
// taken to share a limit.
type AccountKey struct {
	Harness          string                  `json:"harness"`
	Backend          Backend                 `json:"backend"`
	CredentialSource BackendCredentialSource `json:"credential_source,omitempty"`
	ConfigHome       string                  `json:"config_home,omitempty"`
}

// String renders the key for a view. It is for people; compare keys with ==.
func (k AccountKey) String() string {
	src, home := string(k.CredentialSource), k.ConfigHome
	if src == "" {
		src = "-"
	}
	if home == "" {
		home = "default-home"
	}
	return strings.Join([]string{k.Harness, string(k.Backend), src, home}, " ")
}

// AccountKeyOf derives the key for a session. A session marvel never
// classified (a record from before the backend fields) is taken to be on the
// default backend, which is where a subscription resolves too.
func AccountKeyOf(s Session) AccountKey {
	b := s.BackendResolved
	if b == "" {
		b = BackendDefaultName
	}
	return AccountKey{
		Harness:          s.Runtime.Name,
		Backend:          b,
		CredentialSource: s.BackendCredentialSource,
		ConfigHome:       s.AccountHome,
	}
}

// AccountWindow is one rate-limit window of an account. UsedPercent is a
// pointer because a window can arrive without one, and that must not read as
// zero percent.
type AccountWindow struct {
	Name        string    `json:"name"`
	UsedPercent *float64  `json:"used_percent,omitempty"`
	ResetsAt    time.Time `json:"resets_at,omitempty"`
}

// AccountReading is the newest usable reading for one account.
type AccountReading struct {
	Windows []AccountWindow `json:"windows"`
	Session string          `json:"session"`
	At      time.Time       `json:"at"`
}

// AccountLimitsRequest is the account.limits RPC body. It names the reporting
// session, which the daemon resolves to the account key, and carries the
// windows. It is not the heartbeat request and shares none of its fields.
type AccountLimitsRequest struct {
	Session string          `json:"session"`
	Windows []AccountWindow `json:"windows"`
}

// AccountReadings holds the newest reading per account, in memory.
type AccountReadings struct {
	mu sync.Mutex
	m  map[AccountKey]AccountReading
}

// NewAccountReadings returns an empty store.
func NewAccountReadings() *AccountReadings {
	return &AccountReadings{m: map[AccountKey]AccountReading{}}
}

// Record stores a reading and reports whether it was usable. A window with no
// percentage is dropped; a reading with no usable window is not stored, and
// does not replace an earlier one.
func (a *AccountReadings) Record(key AccountKey, windows []AccountWindow, session string, at time.Time) bool {
	var usable []AccountWindow
	for _, w := range windows {
		if w.UsedPercent == nil || math.IsNaN(*w.UsedPercent) || math.IsInf(*w.UsedPercent, 0) || *w.UsedPercent < 0 {
			continue
		}
		usable = append(usable, cloneWindow(w))
	}
	if len(usable) == 0 {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.m[key] = AccountReading{Windows: usable, Session: session, At: at}
	return true
}

// Reading returns the newest reading for key and its state at now. The
// returned value is a copy.
func (a *AccountReadings) Reading(key AccountKey, now time.Time) (AccountReading, ReadingState) {
	a.mu.Lock()
	r, ok := a.m[key]
	a.mu.Unlock()
	if !ok {
		return AccountReading{}, ReadingNone
	}
	out := AccountReading{Session: r.Session, At: r.At, Windows: make([]AccountWindow, len(r.Windows))}
	for i, w := range r.Windows {
		out.Windows[i] = cloneWindow(w)
	}
	if now.Sub(r.At) > ReadingMaxAge {
		return out, ReadingStale
	}
	return out, ReadingFresh
}

// Keys lists every account with a stored reading, in a stable order.
func (a *AccountReadings) Keys() []AccountKey {
	a.mu.Lock()
	keys := make([]AccountKey, 0, len(a.m))
	for k := range a.m {
		keys = append(keys, k)
	}
	a.mu.Unlock()
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	return slices.Clip(keys)
}

func cloneWindow(w AccountWindow) AccountWindow {
	if w.UsedPercent != nil {
		v := *w.UsedPercent
		w.UsedPercent = &v
	}
	return w
}
