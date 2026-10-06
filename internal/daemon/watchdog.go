package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/panestate"
	"github.com/arcavenae/marvel/internal/runtime"
)

// WatchdogInterval is how often the harness-state watchdog looks at the gate.
// A capture still happens at most once per window per session.
const WatchdogInterval = 30 * time.Second

// DefaultWatchdogWindow is how long a session must be quiet before it is a
// candidate (docs/design/harness-state-watchdog-p1.md section 3).
const DefaultWatchdogWindow = api.DefaultQuietWindow

// rollupSeats is how many seats of one account reading logged-out make one
// account.logged-out event.
const rollupSeats = 3

// watchdog classifies a quiet pane as logged-out and surfaces it. It never
// sends a key, injects text, restarts a session, or changes State or
// HealthState: the only store write is Session.HarnessState (test 10 pins it).
type watchdog struct {
	store  *api.Store
	ring   *events.Ring
	sets   []panestate.Pattern
	window time.Duration
	reg    *runtime.Registry
	now    func() time.Time

	// readPane returns the pane's foreground command and width; capture
	// returns its visible screen with wrapped rows joined. Both are injected
	// so tests need no tmux server.
	readPane func(paneID string) (string, int, error)
	capture  func(paneID string) (string, error)

	// sample returns a pattern's captured screen, for the control that runs
	// once at start. Injected so a test needs no embedded sample.
	sample func(panestate.Pattern) (string, error)

	// failed holds the harness and version of every pattern that failed its
	// control and left its version with no passing pattern, as
	// "<harness>/<version>", so the seat on it reads control-failed and not
	// uncovered.
	failed map[string]bool

	mu          sync.Mutex
	lastCapture map[string]time.Time
	rolled      map[string]string

	// uncovered holds, per "<harness>/<version>", how many examined sessions
	// were on that uncovered version at the last pass. A key is present while
	// the set is non-empty and gone when it empties, so a count change alone is
	// not a transition.
	uncovered map[string]int
}

func newWatchdog(store *api.Store, ring *events.Ring, sets []panestate.Pattern, window time.Duration) *watchdog {
	if window <= 0 {
		window = DefaultWatchdogWindow
	}
	return &watchdog{
		store: store, ring: ring, sets: sets, window: window,
		sample: panestate.EmbeddedSample,
		reg:    runtime.NewRegistry(), now: func() time.Time { return time.Now().UTC() },
		lastCapture: map[string]time.Time{}, rolled: map[string]string{}, failed: map[string]bool{},
		uncovered: map[string]int{},
	}
}

// control runs each pattern against its own sample once, keeps the patterns
// that pass, and says so: one watchdog.control event per pattern (info on a
// pass, warning on a fail, never sample text) and a log line. It runs at
// start, never per pass, so a failed pattern can never match a pane
// (docs/design/watchdog-control-and-uncovered.md sections 2, 3 and 5).
func (w *watchdog) control() {
	results := panestate.Control(w.sets, w.sample)
	kept := make([]panestate.Pattern, 0, len(w.sets))
	passed := map[string]int{}
	var order []string
	for i, res := range results {
		who := fmt.Sprintf("%s %s", res.Harness, res.HarnessVersion)
		label := fmt.Sprintf("pattern %s@%d for %s", res.PatternID, res.PatternVersion, who)
		if res.Pass {
			kept = append(kept, w.sets[i])
			if passed[who] == 0 {
				order = append(order, who)
			}
			passed[who]++
			events.Emit(w.ring, events.Event{
				Kind: events.KindWatchdogControl, Severity: events.SeverityInfo,
				Message: "control passed: " + label,
			})
			continue
		}
		why := fmt.Sprintf("result %s, confidence %q", res.State, res.Confidence)
		if res.Err != nil {
			why = "sample error: " + res.Err.Error()
		}
		events.Emit(w.ring, events.Event{
			Kind: events.KindWatchdogControl, Severity: events.SeverityWarning,
			Message: fmt.Sprintf("control failed: %s: %s", label, why),
		})
		log.Printf("watchdog: control failed for %s pattern %s@%d: %s", who, res.PatternID, res.PatternVersion, why)
	}
	for _, who := range order {
		n := passed[who]
		noun := "patterns"
		if n == 1 {
			noun = "pattern"
		}
		log.Printf("watchdog: controls passed for %s (%d %s)", who, n, noun)
	}
	w.sets = kept
	// A version whose patterns all failed is control-failed, not uncovered:
	// the watchdog was meant to cover it and cannot.
	w.failed = map[string]bool{}
	for _, res := range results {
		if !res.Pass && !versionCovered(kept, res.Harness, res.HarnessVersion) {
			w.failed[res.Harness+"/"+res.HarnessVersion] = true
		}
	}
}

// versionCovered reports whether sets holds a pattern for the harness version.
func versionCovered(sets []panestate.Pattern, harness, version string) bool {
	for _, p := range sets {
		if p.Harness == harness && p.HarnessVersion == version {
			return true
		}
	}
	return false
}

// coveredVersions lists the versions of a harness that have a passing pattern,
// sorted.
func coveredVersions(sets []panestate.Pattern, harness string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range sets {
		if p.Harness == harness && !seen[p.HarnessVersion] {
			seen[p.HarnessVersion] = true
			out = append(out, p.HarnessVersion)
		}
	}
	sort.Strings(out)
	return out
}

// coverage says what the watchdog could do for a seat on this harness version:
// "" when a passing pattern covers it, otherwise the coverage state. A harness
// with no pattern at all, passing or failed, is never claimed: the empty answer
// there means "not examined", and ok is false.
func (w *watchdog) coverage(harness, version string) (state string, ok bool) {
	if versionCovered(w.sets, harness, version) {
		return "", true
	}
	if w.failed[harness+"/"+version] {
		return api.HarnessStateControlFailed, true
	}
	if len(coveredVersions(w.sets, harness)) > 0 || w.harnessFailed(harness) {
		return api.HarnessStateUncovered, true
	}
	return "", false
}

// harnessFailed reports whether any pattern of the harness failed its control.
func (w *watchdog) harnessFailed(harness string) bool {
	for k := range w.failed {
		if strings.HasPrefix(k, harness+"/") {
			return true
		}
	}
	return false
}

// Run looks every WatchdogInterval until ctx is cancelled.
func (w *watchdog) Run(ctx context.Context) {
	t := time.NewTicker(WatchdogInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.Once()
		}
	}
}

// Once runs one pass over every session.
func (w *watchdog) Once() {
	if len(w.sets) == 0 && len(w.failed) == 0 {
		// No pattern set, nothing can match, so nothing is read: no pane is
		// queried and none is captured. The loop guard in startWatchdog says the
		// same; this one holds if that is ever bypassed. A set whose every
		// pattern failed its control is not empty: its seats read control-failed.
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	seen := map[string]bool{}
	for _, s := range w.store.ListSessions() {
		key := s.Key()
		seen[key] = true
		w.visit(now, s)
	}
	for k := range w.lastCapture {
		if !seen[k] {
			delete(w.lastCapture, k)
		}
	}
	w.uncoveredTransitions()
	w.rollup(now)
}

func (w *watchdog) visit(now time.Time, s api.Session) {
	key := s.Key()
	if s.State != api.SessionRunning || s.PaneID == "" {
		w.clear(s, "session ended")
		delete(w.lastCapture, key)
		return
	}
	// The did-work clear is for a state that describes the screen the seat was
	// looking at. A coverage state describes the version, not the screen, so it
	// stays: a seat that alternates work and quiet would otherwise clear and
	// re-set it every cycle.
	if hs := s.HarnessState; hs != nil && !isCoverageState(hs.State) && s.ContextAt.After(hs.ContextAt) {
		w.clear(s, "the session did work")
		return
	}
	adapter := w.reg.Resolve(s.Runtime.Name)
	rule, ok := adapter.(runtime.ForegroundRule)
	if !ok {
		return
	}
	quietSince := s.ContextAt
	if quietSince.IsZero() {
		quietSince = s.CreatedAt
	}
	if quietSince.IsZero() || now.Sub(quietSince) <= w.window {
		return
	}
	if last, ok := w.lastCapture[key]; ok && now.Sub(last) < w.window {
		return
	}
	cmd, width, err := w.readPane(s.PaneID)
	if err != nil {
		return
	}
	version, front := rule.Foreground(cmd)
	if !front {
		return
	}
	w.lastCapture[key] = now
	// Coverage is checked before the screen is read: a version with no passing
	// pattern has nothing to match, so it is not captured, and only a covered
	// version reaches Classify and its no-match clear.
	if state, ok := w.coverage(adapter.Name(), version); !ok {
		return
	} else if state != "" {
		w.setCoverage(now, s, state, adapter.Name(), version, width)
		return
	}
	raw, err := w.capture(s.PaneID)
	if err != nil {
		log.Printf("watchdog: %s: pane capture failed", key)
		return
	}
	rows, err := panestate.Normalize(s.PaneID, raw)
	if err != nil {
		log.Printf("watchdog: %s: %v", key, err)
		return
	}
	res := panestate.Classify(w.sets, adapter.Name(), version, rows)
	switch {
	case res.Confidence == "":
		w.clear(s, "the block no longer matches")
	case res.State == panestate.StateLoggedOut:
		w.set(now, s, res, adapter.Name(), version, width)
	default:
		w.clearEvent(s, "the block no longer fully matches")
		w.store.SetHarnessState(key, harnessState(now, s, res, adapter.Name(), version, width))
	}
}

// uncoveredTransitions emits watchdog.uncovered once per harness version when
// the set of examined sessions on an uncovered version goes from empty to
// non-empty, naming the count then, and the cleared form when it goes back to
// empty or a pattern set for the version loads. A count that changes while the
// set stays non-empty emits nothing, so a point release reads as one fact
// (docs/design/watchdog-control-and-uncovered.md section 5). Only the
// uncovered state counts: control-failed has its own warning at start. An
// ended session has already had its state cleared by visit, so it is not
// counted.
func (w *watchdog) uncoveredTransitions() {
	now := map[string]int{}
	names := map[string][2]string{}
	for _, s := range w.store.ListSessions() {
		hs := s.HarnessState
		if hs == nil || hs.State != api.HarnessStateUncovered {
			continue
		}
		if versionCovered(w.sets, hs.Harness, hs.HarnessVersion) {
			continue
		}
		k := hs.Harness + "/" + hs.HarnessVersion
		now[k]++
		names[k] = [2]string{hs.Harness, hs.HarnessVersion}
	}
	for _, k := range slices.Sorted(maps.Keys(now)) {
		if _, was := w.uncovered[k]; was {
			continue
		}
		h, v := names[k][0], names[k][1]
		noun := "sessions"
		if now[k] == 1 {
			noun = "session"
		}
		events.Emit(w.ring, events.Event{
			Kind: events.KindWatchdogUncovered, Severity: events.SeverityInfo,
			Message: fmt.Sprintf("uncovered: %s %s, %d %s with no passing pattern (covered: %s)",
				h, v, now[k], noun, coveredList(coveredVersions(w.sets, h))),
		})
	}
	for _, k := range slices.Sorted(maps.Keys(w.uncovered)) {
		if _, still := now[k]; still {
			continue
		}
		events.Emit(w.ring, events.Event{
			Kind: events.KindWatchdogUncovered, Severity: events.SeverityInfo,
			Message: "uncovered cleared: " + strings.Replace(k, "/", " ", 1),
		})
	}
	w.uncovered = now
}

func coveredList(v []string) string {
	if len(v) == 0 {
		return "none"
	}
	return strings.Join(v, ", ")
}

func isCoverageState(state string) bool {
	return state == api.HarnessStateUncovered || state == api.HarnessStateControlFailed
}

// setCoverage records that no passing pattern covers the seat's version. It
// writes only when the verdict is new or changed, so a pass that finds the same
// fact leaves the stored state as it was.
func (w *watchdog) setCoverage(now time.Time, s api.Session, state, harness, version string, width int) {
	covered := coveredVersions(w.sets, harness)
	if hs := s.HarnessState; hs != nil && hs.State == state && hs.Harness == harness &&
		hs.HarnessVersion == version && slices.Equal(hs.Covered, covered) {
		return
	}
	w.clearEvent(s, "the version is not covered")
	w.store.SetHarnessState(s.Key(), &api.HarnessState{
		State: state, Harness: harness, HarnessVersion: version, Covered: covered,
		PaneWidth: width, CapturedAt: now, ContextAt: s.ContextAt,
	})
}

func harnessState(now time.Time, s api.Session, res panestate.Result, harness, version string, width int) *api.HarnessState {
	return &api.HarnessState{
		State: string(res.State), Confidence: string(res.Confidence),
		Harness: harness, HarnessVersion: version,
		PatternID: res.PatternID, PatternVersion: res.PatternVersion, PatternFor: res.HarnessVersion,
		PaneWidth: width, Evidence: res.Evidence, CapturedAt: now, ContextAt: s.ContextAt,
	}
}

func (w *watchdog) set(now time.Time, s api.Session, res panestate.Result, harness, version string, width int) {
	hs := harnessState(now, s, res, harness, version, width)
	already := s.HarnessState != nil && s.HarnessState.State == api.HarnessStateLoggedOut
	w.store.SetHarnessState(s.Key(), hs)
	if already {
		return
	}
	events.Emit(w.ring, events.Event{
		Kind: events.KindSessionHarnessState, Severity: events.SeverityWarning,
		Workspace: s.Workspace, Team: s.Team, Role: s.Role, Session: s.Name,
		Message: fmt.Sprintf("%s %s (%s, %s %s, pattern %s@%d, width %d): %s",
			api.HarnessStateLoggedOut, s.Name, res.Confidence, harness, versionOrUnknown(version),
			res.PatternID, res.PatternVersion, width, strings.Join(res.Evidence, " / ")),
	})
}

func versionOrUnknown(v string) string {
	if v == "" {
		return "version unknown"
	}
	return v
}

// clearEvent emits the cleared event when a logged-out state is ending.
func (w *watchdog) clearEvent(s api.Session, why string) {
	if s.HarnessState == nil || s.HarnessState.State != api.HarnessStateLoggedOut {
		return
	}
	events.Emit(w.ring, events.Event{
		Kind: events.KindSessionHarnessStateCleared, Severity: events.SeverityInfo,
		Workspace: s.Workspace, Team: s.Team, Role: s.Role, Session: s.Name,
		Message: fmt.Sprintf("logged-out cleared for %s: %s", s.Name, why),
	})
}

func (w *watchdog) clear(s api.Session, why string) {
	if s.HarnessState == nil {
		return
	}
	w.clearEvent(s, why)
	w.store.SetHarnessState(s.Key(), nil)
}

// accountLabel groups seats by runtime and config directory. The directory is
// reduced to a short hash so no path reaches an event.
func accountLabel(s api.Session) string {
	home, _ := os.UserHomeDir()
	dir := api.CanonicalConfigDir(s.Runtime.Name, s.Runtime.Env["CLAUDE_CONFIG_DIR"], home)
	if dir == "" {
		return s.Runtime.Name + ":default"
	}
	sum := sha256.Sum256([]byte(dir))
	return s.Runtime.Name + ":" + hex.EncodeToString(sum[:4])
}

// rollup emits one account.logged-out when three or more seats of one account
// read logged-out within one window, and again only when that set changes.
func (w *watchdog) rollup(now time.Time) {
	groups := map[string][]string{}
	for _, s := range w.store.ListSessions() {
		hs := s.HarnessState
		if hs == nil || hs.State != api.HarnessStateLoggedOut || s.State != api.SessionRunning {
			continue
		}
		if now.Sub(hs.CapturedAt) > w.window {
			continue
		}
		l := accountLabel(s)
		groups[l] = append(groups[l], s.Key())
	}
	for l := range w.rolled {
		if len(groups[l]) < rollupSeats {
			delete(w.rolled, l)
		}
	}
	for l, keys := range groups {
		if len(keys) < rollupSeats {
			continue
		}
		sort.Strings(keys)
		sig := strings.Join(keys, ",")
		if w.rolled[l] == sig {
			continue
		}
		w.rolled[l] = sig
		events.Emit(w.ring, events.Event{
			Kind: events.KindAccountLoggedOut, Severity: events.SeverityWarning,
			Message: fmt.Sprintf("%d seats on account %s read logged-out: %s", len(keys), l, strings.Join(keys, ", ")),
		})
	}
}

// watchdogFor builds the watchdog and runs its control once, or returns nil
// when there is no pattern set: with none, nothing can match, so no loop is
// started and no pane is touched.
// Split out so a test can pin that the empty set starts nothing.
func watchdogFor(store *api.Store, ring *events.Ring, sets []panestate.Pattern, window time.Duration) *watchdog {
	if len(sets) == 0 {
		return nil
	}
	w := newWatchdog(store, ring, sets, window)
	w.control()
	return w
}

// startWatchdog runs the harness-state watchdog beside the metrics sampler.
// Only the shipped pattern sets are read: with none, nothing can match, so the
// loop is not started at all.
func (d *Daemon) startWatchdog(ctx context.Context) {
	sets, err := panestate.LoadEmbedded()
	if err != nil {
		log.Printf("watchdog: pattern sets unreadable, watchdog off: %v", err)
		return
	}
	var window time.Duration
	if cfg, cerr := config.Load(); cfg != nil {
		var werr error
		if window, werr = cfg.WatchdogWindow(); werr != nil {
			log.Printf("watchdog: %v, using the default", werr)
			window = 0
		}
	} else if cerr != nil {
		log.Printf("watchdog: client config unreadable, using the default window: %v", cerr)
	}
	w := watchdogFor(d.store, d.events, sets, window)
	if w == nil {
		return
	}
	w.readPane = d.driver.PaneForeground
	w.capture = d.driver.CapturePaneJoined
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		w.Run(ctx)
	}()
}
