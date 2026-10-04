package api

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
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
		ConfigHome:       canonicalConfigHome(s.Runtime.Name, s.AccountHome),
	}
}

// defaultHomeDirs is each harness's own default state directory, relative to
// the user's home. Naming it explicitly and naming nothing are the same login.
var defaultHomeDirs = map[string]string{"claude": ".claude", "codex": ".codex"}

// canonicalConfigHome reduces a recorded login home to one spelling: empty for
// the harness's default directory, otherwise the cleaned path. Without it a
// session that fell back to the shared home (empty) and one that names the
// same directory (codex always does) would be two accounts for one login.
func canonicalConfigHome(harness, home string) string {
	userHome, err := os.UserHomeDir()
	if err != nil {
		userHome = ""
	}
	return canonicalConfigHomeIn(harness, home, userHome)
}

func canonicalConfigHomeIn(harness, home, userHome string) string {
	if home == "" {
		return ""
	}
	home = filepath.Clean(home)
	if def, ok := defaultHomeDirs[harness]; ok && userHome != "" && home == filepath.Join(userHome, def) {
		return ""
	}
	return home
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
	// unstamped marks a reading whose sender gave no observation time, so At is
	// only when the daemon received it. Such a reading never displaces a stamped
	// one and is displaced by any later reading.
	unstamped bool
}

// AccountLimitsRequest is the account.limits RPC body. It names the reporting
// session, which the daemon resolves to the account key, and carries the
// windows. It is not the heartbeat request and shares none of its fields.
type AccountLimitsRequest struct {
	Session string `json:"session"`
	// SessionToken is the secret marvel minted for the reporting session at
	// spawn (MARVEL_HEARTBEAT_TOKEN). A reading can set the limited condition
	// on every session of an account, so the daemon accepts it only from a
	// process that holds the token of the session it names.
	SessionToken string          `json:"session_token,omitempty"`
	Windows      []AccountWindow `json:"windows"`
	// ObservedAt is when the harness made the observation, when the sender
	// knows (codex stamps each rollout record). Zero means "now", which is
	// right for a statusline tick. The daemon keeps the newest reading by this
	// time, so a sender re-posting an old figure cannot replace a newer one.
	// Additive: an older daemon ignores it, an older sender omits it.
	ObservedAt time.Time `json:"observed_at,omitempty"`
}

// ErrAccountReportUnbound is returned when a reading names a session whose
// record carries no token. Unlike the heartbeat, which admits that case for
// records written before tokens existed, a reading that can mark a whole
// account limited is refused: such a session has no token to present, and the
// record drains as those sessions end.
var ErrAccountReportUnbound = errors.New("session carries no token, so a reading cannot be bound to it")

// AuthenticateAccountReport returns the session a reading names, provided the
// presented token is the one minted for it. A missing session is ErrNotFound,
// a record with no token hash is ErrAccountReportUnbound, and a wrong or absent
// token is ErrHeartbeatUnauthorized. The comparison is constant-time.
func (s *Store) AuthenticateAccountReport(sessionKey, token string) (Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[sessionKey]
	if !ok {
		return Session{}, fmt.Errorf("session %s: %w", sessionKey, ErrNotFound)
	}
	if sess.HeartbeatTokenHash == "" {
		return Session{}, fmt.Errorf("session %s: %w", sessionKey, ErrAccountReportUnbound)
	}
	presented := HashHeartbeatToken(token)
	if subtle.ConstantTimeCompare([]byte(presented), []byte(sess.HeartbeatTokenHash)) != 1 {
		return Session{}, fmt.Errorf("session %s: %w", sessionKey, ErrHeartbeatUnauthorized)
	}
	return cloneSession(sess), nil
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
	return a.RecordObserved(key, windows, session, at, at)
}

// RecordObserved is Record with the receiving clock. A stamped reading that is
// no longer fresh at now does not displace a held unstamped one: the stamp says
// the figure is old, and the unstamped reading is the daemon's newest news.
func (a *AccountReadings) RecordObserved(key AccountKey, windows []AccountWindow, session string, at, now time.Time) bool {
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
	// The newest observation wins. An older one is not an error, it is
	// information the account already has newer.
	if held, ok := a.m[key]; ok {
		if !held.unstamped && at.Before(held.At) {
			return false
		}
		if held.unstamped && now.Sub(at) > ReadingMaxAge {
			return false
		}
	}
	a.m[key] = AccountReading{Windows: usable, Session: session, At: at}
	return true
}

// RecordUnstamped stores a reading whose sender gave no observation time. The
// time is unknown, so it is treated as unknown: it is stored only when nothing
// stamped is held (an older sender that never stamps still gets its readings
// through, one replacing the last), and it never displaces a stamped reading.
// now is when the daemon received it.
func (a *AccountReadings) RecordUnstamped(key AccountKey, windows []AccountWindow, session string, now time.Time) bool {
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
	if held, ok := a.m[key]; ok && !held.unstamped {
		return false
	}
	a.m[key] = AccountReading{Windows: usable, Session: session, At: now, unstamped: true}
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
