package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/arcavenae/marvel/contracts/go/envelope"
	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/team"
)

const (
	// accountWarnState is the STATE word of a fresh account window at or over the
	// cluster's warning level in get budgets.
	accountWarnState = "near-limit"
	// accountWarnRetry is the least time between two tries to publish one
	// window's notice to the director.
	accountWarnRetry = 5 * time.Minute
	// accountWarnJitter is how far a window's resets_at may move and still be the
	// same window, so two senders that round it differently do not read as two.
	accountWarnJitter = time.Minute
	// accountWarnPublishTimeout bounds one publish, so a publish to a dead bus ends
	// on its own. It runs off the reporting call and delays no reading.
	accountWarnPublishTimeout = 2 * time.Second
	// accountWarnSender is the notice's sender agent id, a label and not an
	// authentication.
	accountWarnSender = "marvel"
	// accountWarnRecipient is the director's recipient address on the envelope.
	accountWarnRecipient = "global://director"
)

// accountWarner reports an account window reaching the cluster's warning level,
// once per window: one ring event and, when the director's inbox can be
// reached, one INFORM to it. It reads and reports. Nothing pauses, sheds,
// refuses or blocks on it (diagnostic-not-gate). Its memory is in the daemon
// process, so a daemon restart can say it again for a window already announced.
type accountWarner struct {
	level  int
	events events.Emitter
	send   func(ctx context.Context, env envelope.Envelope, msgID string) error
	leafUp func() bool

	mu   sync.Mutex
	seen map[warnWindowKey]*warnWindow
	// wg counts the publishes in flight. A publish runs off the reporting call, so
	// a slow bus never holds the reading that caused it.
	wg sync.WaitGroup
}

// warnWindowKey names a window of one account.
type warnWindowKey struct {
	account api.AccountKey
	window  string
}

// warnWindow is what the warner remembers of the window it last announced.
type warnWindow struct {
	resetsAt time.Time
	// armedAt is when this window was first held. A window with no resets_at has
	// no other name, so the time it was armed keeps one arming's dedupe id apart
	// from the next arming's.
	armedAt     time.Time
	sent        bool
	lastAttempt time.Time
}

// newAccountWarner builds the warner. level 0 is off. send publishes one
// envelope to the director's inbox, and nil means there is no bus. leafUp says
// whether a publish may be tried.
func newAccountWarner(level int, em events.Emitter, send func(ctx context.Context, env envelope.Envelope, msgID string) error, leafUp func() bool) *accountWarner {
	return &accountWarner{level: level, events: em, send: send, leafUp: leafUp, seen: map[warnWindowKey]*warnWindow{}}
}

// observe is called with each fresh reading just stored for key, on the daemon's
// clock. workspace is the reporting seat's, for the notice's sender.
func (w *accountWarner) observe(key api.AccountKey, workspace string, windows []api.AccountWindow, now time.Time) {
	if w == nil || w.level <= 0 {
		return
	}
	for _, win := range windows {
		if win.UsedPercent == nil || math.IsNaN(*win.UsedPercent) {
			continue
		}
		w.observeWindow(key, workspace, win, *win.UsedPercent, now)
	}
}

func (w *accountWarner) observeWindow(key api.AccountKey, workspace string, win api.AccountWindow, pct float64, now time.Time) {
	k := warnWindowKey{account: key, window: win.Name}
	w.mu.Lock()
	held := w.seen[k]
	if pct < float64(w.level) {
		// A window with no resets_at has no end to tell it apart from the next, so
		// it ends when the reading falls back under the level.
		if held != nil && win.ResetsAt.IsZero() {
			delete(w.seen, k)
		}
		w.mu.Unlock()
		return
	}
	if held != nil && !sameWarnWindow(held.resetsAt, win.ResetsAt) {
		held = nil
	}
	first := held == nil
	if first {
		held = &warnWindow{resetsAt: win.ResetsAt, armedAt: now}
		w.seen[k] = held
	}
	due := !held.sent && w.send != nil && w.leafUp != nil && w.leafUp() &&
		(held.lastAttempt.IsZero() || now.Sub(held.lastAttempt) >= accountWarnRetry)
	if due {
		held.lastAttempt = now
		w.wg.Add(1)
	}
	armedAt := held.armedAt
	w.mu.Unlock()

	if first {
		w.emit(key, workspace, win, pct)
	}
	if !due {
		return
	}
	env, msgID := accountWarnEnvelope(key, workspace, win, pct, now, armedAt)
	go func() {
		defer w.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), accountWarnPublishTimeout)
		err := w.send(ctx, env, msgID)
		cancel()
		if err != nil {
			log.Printf("account warn: %s %s: publish to the director: %v; trying again after %s", key, win.Name, err, accountWarnRetry)
			return
		}
		w.mu.Lock()
		held.sent = true
		w.mu.Unlock()
	}()
}

// wait blocks until the publishes now in flight have ended. Tests use it to read
// what was sent; nothing else needs to.
func (w *accountWarner) wait() {
	if w != nil {
		w.wg.Wait()
	}
}

// sameWarnWindow reports whether two resets_at values name one window: both
// absent, or within the jitter allowance of each other.
func sameWarnWindow(a, b time.Time) bool {
	if a.IsZero() || b.IsZero() {
		return a.IsZero() && b.IsZero()
	}
	d := b.Sub(a)
	if d < 0 {
		d = -d
	}
	return d < accountWarnJitter
}

func (w *accountWarner) emit(key api.AccountKey, workspace string, win api.AccountWindow, pct float64) {
	events.Emit(w.events, events.Event{
		Kind:      events.KindAccountWindowNearLimit,
		Severity:  events.SeverityWarning,
		Workspace: workspace,
		Message:   accountWarnText(key, win, pct),
	})
}

func accountWarnText(key api.AccountKey, win api.AccountWindow, pct float64) string {
	resets := "no reset time reported"
	if !win.ResetsAt.IsZero() {
		resets = "resets " + win.ResetsAt.UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf("account %s: %s window at %.0f%% of its limit, %s", key, win.Name, pct, resets)
}

// accountWarnEnvelope builds the INFORM to the director and its dedupe id. The
// id names the window, so a re-send after a crash between publish and record is
// dropped by the stream. A window with no resets_at is named by the time it was
// armed, so a later arming of the same window is a new notice.
func accountWarnEnvelope(key api.AccountKey, workspace string, win api.AccountWindow, pct float64, now, armedAt time.Time) (envelope.Envelope, string) {
	slot := fmt.Sprintf("%d", win.ResetsAt.Unix())
	if win.ResetsAt.IsZero() {
		slot = fmt.Sprintf("armed-%d", armedAt.Unix())
	}
	corr := fmt.Sprintf("account-window-near-limit/%s/%s/%s", key, win.Name, slot)
	env := envelope.Envelope{
		SchemaVersion: 1,
		MessageID:     team.NewULID(now),
		CorrelationID: &corr,
		Sender:        envelope.Sender{AgentID: accountWarnSender, Workspace: workspace},
		Recipient:     envelope.Recipient{Address: accountWarnRecipient},
		Performative:  envelope.PerformativeINFORM,
		Authority:     &envelope.Authority{Strength: envelope.StrengthNone},
		Content:       envelope.Content{Type: envelope.TypeSignal, Data: accountWarnText(key, win, pct)},
		SentAt:        now.UTC(),
	}
	return env, corr
}

// loadAccountWarn builds the daemon's account warner from the cluster entry
// whose socket is ours, found the way loadHostVitals finds it. An unset key is
// the default level, 0 is off, and a value outside 0 to 100 is logged and reads
// as the default. A config that cannot be read at all leaves the default.
func (d *Daemon) loadAccountWarn(socketPath string) {
	var cl *config.Cluster
	if cfg, err := config.Load(); configLoadUsable(err) && cfg != nil {
		cl = cfg.ClusterForSocket(socketPath)
	}
	level, err := cl.AccountWarnLevel()
	if err != nil {
		log.Printf("account warn: cluster %s: %v; using the default %d", cl.Name, err, config.DefaultAccountWarnPercent)
		level = config.DefaultAccountWarnPercent
	}
	d.accountWarnLevel = level
	d.accountWarn = newAccountWarner(level, d.events, d.sendAccountWarn, d.accountWarnLeafUp)
}

// sendAccountWarn publishes one envelope to the director's inbox through the
// team controller's publisher, which is nil while the cluster has no managed
// bus.
func (d *Daemon) sendAccountWarn(ctx context.Context, env envelope.Envelope, msgID string) error {
	p := d.teamCtrl.Publisher
	if p == nil {
		return errors.New("no managed bus")
	}
	return p.PublishDirectorInbox(ctx, env, msgID)
}

// accountWarnLeafUp is the controller's own gate for the director leg: the
// notice is tried only while the leaf is up.
func (d *Daemon) accountWarnLeafUp() bool {
	return d.teamCtrl.LeafUp != nil && d.teamCtrl.LeafUp()
}
