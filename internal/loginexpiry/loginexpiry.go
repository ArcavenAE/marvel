// Package loginexpiry reads when a harness login expires, by asking the
// harness's own status command, and tracks each login's state so a warning
// is raised once, ahead of the lapse (docs/design/native-checks.md section 2).
//
// It reads no credential. The status command's answer carries an email and an
// organization next to the three fields wanted; Decode keeps the three and
// drops the rest, and no output text reaches an error, an event or a log.
package loginexpiry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// MaxOutput is the most stdout Ask and Decode read.
const MaxOutput = 64 << 10

// WarnWithin is how far ahead of the expiry the warning is raised.
const WarnWithin = 72 * time.Hour

// Reading is the three fields kept from the status command's answer.
type Reading struct {
	LoggedIn     bool
	ExpiresAt    time.Time
	Subscription string
}

// Reason is why a read gave no reading. The set is fixed so no output text can
// reach an error.
type Reason string

const (
	ReasonTimedOut      Reason = "timed out"
	ReasonExitNonZero   Reason = "exited non-zero"
	ReasonNotJSON       Reason = "not JSON"
	ReasonExpiryMissing Reason = "expiry missing"
)

// ReadError is a read that gave no reading.
type ReadError struct{ Reason Reason }

func (e *ReadError) Error() string { return "login status read failed: " + string(e.Reason) }

// Decode reads the status command's stdout and keeps loginExpiresAt,
// loggedIn and subscriptionType. The rest of the answer (an email, an
// organization) is dropped by the decoder and never held. A logged-out answer
// needs no expiry; a logged-in one must carry one.
func Decode(r io.Reader) (Reading, error) {
	raw, err := io.ReadAll(io.LimitReader(r, MaxOutput+1))
	if err != nil || len(raw) > MaxOutput {
		return Reading{}, &ReadError{Reason: ReasonNotJSON}
	}
	var doc struct {
		LoggedIn         *bool  `json:"loggedIn"`
		LoginExpiresAt   string `json:"loginExpiresAt"`
		SubscriptionType string `json:"subscriptionType"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || doc.LoggedIn == nil {
		return Reading{}, &ReadError{Reason: ReasonNotJSON}
	}
	rd := Reading{LoggedIn: *doc.LoggedIn, Subscription: doc.SubscriptionType}
	if exp, err := time.Parse(time.RFC3339Nano, doc.LoginExpiresAt); err == nil {
		rd.ExpiresAt = exp
	} else if rd.LoggedIn {
		return Reading{}, &ReadError{Reason: ReasonExpiryMissing}
	}
	return rd, nil
}

// waitDelay is how long Ask waits for the output pipe after the child has
// gone: the harness forks the keychain reader, and a grandchild that holds
// the pipe open must not hang the daemon.
const waitDelay = 500 * time.Millisecond

// capWriter keeps at most MaxOutput+1 bytes, so Decode can tell an answer over
// the cap, and discards the rest.
type capWriter struct{ buf bytes.Buffer }

func (c *capWriter) Write(p []byte) (int, error) {
	if room := MaxOutput + 1 - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// Ask runs `<program> auth status --json` with CLAUDE_CONFIG_DIR set to
// configDir exactly as given (removed from the environment when configDir is
// empty), bounded by bound, and decodes the answer. stderr goes to /dev/null.
// The child runs in its own process group and the whole group is killed on the
// bound and after the call, so a grandchild cannot outlive it. It reads no
// credential: the harness answers from its own store.
func Ask(ctx context.Context, program, configDir string, bound time.Duration) (Reading, error) {
	ctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, "auth", "status", "--json")
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") {
			env = append(env, kv)
		}
	}
	if configDir != "" {
		env = append(env, "CLAUDE_CONFIG_DIR="+configDir)
	}
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var out capWriter
	cmd.Stdout = &out
	cmd.Stderr = nil // /dev/null
	cmd.WaitDelay = waitDelay
	cmd.Cancel = func() error { return killGroup(cmd) }
	if err := cmd.Start(); err != nil {
		return Reading{}, &ReadError{Reason: ReasonExitNonZero}
	}
	werr := cmd.Wait()
	_ = killGroup(cmd)
	// A child that exited 0 on its own has answered, even when a grandchild held
	// the pipe past the bound; one the bound killed has not.
	exited0 := cmd.ProcessState != nil && cmd.ProcessState.Success()
	switch {
	case ctx.Err() != nil && !exited0:
		return Reading{}, &ReadError{Reason: ReasonTimedOut}
	case werr != nil && (!errors.Is(werr, exec.ErrWaitDelay) || !exited0):
		return Reading{}, &ReadError{Reason: ReasonExitNonZero}
	}
	return Decode(bytes.NewReader(out.buf.Bytes()))
}

// killGroup kills the child's process group, only for a pid above 1.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil || cmd.Process.Pid <= 1 {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// State is where one login stands.
type State string

const (
	StateOK         State = "ok"
	StateExpiring   State = "expiring"
	StateLapsed     State = "lapsed"
	StateUnreadable State = "unreadable"
)

// renewalLine is how far later an expiry must move to read as a renewal. The
// harness's own times jitter by a fraction of a second.
const renewalLine = time.Minute

// Row is one login's standing, as a verb or a vitals row shows it.
type Row struct {
	Key       api.AccountKey
	State     State
	ExpiresAt time.Time
	ReadAt    time.Time
	// Why is the fixed reason of the last failed read, empty when it succeeded.
	Why Reason
}

// Tracker holds each login's state and emits an event on each transition. It
// is safe for concurrent use.
type Tracker struct {
	emit events.Emitter
	mu   sync.Mutex
	rows map[api.AccountKey]*Row
}

// NewTracker returns a tracker that emits to emit.
func NewTracker(emit events.Emitter) *Tracker {
	return &Tracker{emit: emit, rows: map[api.AccountKey]*Row{}}
}

// Observe records one read of one login. err is the read's error, nil when rd
// is a reading. A failed read keeps the login's state and says why.
func (t *Tracker) Observe(key api.AccountKey, rd Reading, err error, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	row, seen := t.rows[key]
	if !seen {
		row = &Row{Key: key, State: StateUnreadable}
		t.rows[key] = row
	}
	row.ReadAt = now
	if err != nil {
		row.Why = ReasonExitNonZero
		var re *ReadError
		if errors.As(err, &re) {
			row.Why = re.Reason
		}
		return
	}
	row.Why = ""
	prior, priorExpiry := row.State, row.ExpiresAt
	known := seen && prior != StateUnreadable

	if !rd.LoggedIn || (!rd.ExpiresAt.IsZero() && !rd.ExpiresAt.After(now)) {
		row.State, row.ExpiresAt = StateLapsed, rd.ExpiresAt
		if prior != StateLapsed {
			t.send(events.KindAccountLoginLapsed, events.SeverityError, fmt.Sprintf("%s: login lapsed", key))
		}
		return
	}
	row.ExpiresAt = rd.ExpiresAt
	if rd.ExpiresAt.Sub(now) <= WarnWithin {
		row.State = StateExpiring
	} else {
		row.State = StateOK
	}
	if known && (prior == StateLapsed || rd.ExpiresAt.Sub(priorExpiry) > renewalLine) {
		t.send(events.KindAccountLoginRenewed, events.SeverityInfo,
			fmt.Sprintf("%s: renewed, expires %s", key, rd.ExpiresAt.UTC().Format(time.RFC3339)))
	}
	if row.State == StateExpiring && prior != StateExpiring {
		t.send(events.KindAccountLoginExpiring, events.SeverityWarning,
			fmt.Sprintf("%s: expires %s, %d hours left", key, rd.ExpiresAt.UTC().Format(time.RFC3339), int(rd.ExpiresAt.Sub(now).Hours())))
	}
}

func (t *Tracker) send(kind events.Kind, sev events.Severity, msg string) {
	events.Emit(t.emit, events.Event{Kind: kind, Severity: sev, Message: msg})
}

// Rows lists every login seen, ordered by key string.
func (t *Tracker) Rows() []Row {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Row, 0, len(t.rows))
	for _, r := range t.rows {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key.String() < out[j].Key.String() })
	return out
}
