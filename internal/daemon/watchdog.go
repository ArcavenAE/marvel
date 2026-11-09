package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"path/filepath"
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

	// suspended reports whether the cluster is suspended; a pass returns
	// before it reads any pane while it is. Nil means never.
	suspended func() bool

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

	// failedPats holds the patterns that failed their control, so a seat on any
	// version a failed range pattern would have covered reads control-failed.
	failedPats []panestate.Pattern

	mu          sync.Mutex
	lastCapture map[string]time.Time
	rolled      map[string]string

	// client reports whether a workspace is a client one. The transcript reader
	// skips a client seat until the operator rules on reading client-workspace
	// transcripts, and a workspace it cannot read counts as client. Nil counts
	// every workspace as client, so a watchdog built without the test reads no
	// transcript.
	client func(workspace string) (bool, error)

	// tailRead returns the bytes of a transcript from an offset to its end, at
	// most transcriptTailCap of them. Injected so a test can count reads.
	tailRead func(path string, from int64) ([]byte, error)

	// tx holds, per session key, where the transcript reader left off.
	tx map[string]*transcriptTail

	// uncovered holds, per "<harness>/<version>", how many examined sessions
	// were on that uncovered version at the last pass. A key is present while
	// the set is non-empty and gone when it empties, so a count change alone is
	// not a transition.
	uncovered map[string]int
}

// transcriptTail is what the reader keeps for one seat between ticks: the file
// it read and how far, so an unchanged file costs one stat and a changed one
// costs only its new bytes.
type transcriptTail struct {
	path   string
	size   int64
	mtime  time.Time
	offset int64
	read   panestate.TranscriptReading
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
		tailRead:  readTranscriptTail, tx: map[string]*transcriptTail{},
	}
}

// control runs each pattern against its own sample once, keeps the patterns
// that pass, and says so: one watchdog.control event per pattern (info on a
// pass, warning on a fail, never sample text) and a log line. It runs at
// start, never per pass, so a failed pattern can never match a pane
// (docs/design/watchdog-control-and-uncovered.md sections 2, 3 and 5).
func (w *watchdog) control() {
	all := w.sets
	results := panestate.Control(all, w.sample)
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
	w.failedPats = nil
	for i, res := range results {
		if res.Pass {
			continue
		}
		w.failedPats = append(w.failedPats, all[i])
		if !versionCovered(kept, res.Harness, res.HarnessVersion) {
			w.failed[res.Harness+"/"+res.HarnessVersion] = true
		}
	}
}

// versionCovered reports whether sets holds a pattern for the harness version.
func versionCovered(sets []panestate.Pattern, harness, version string) bool {
	for _, p := range sets {
		if p.Harness == harness && p.Covers(version) {
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
		if p.Harness == harness && !seen[p.VersionLabel()] {
			seen[p.VersionLabel()] = true
			out = append(out, p.VersionLabel())
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
	// A range pattern that failed its control leaves every version in its range
	// unwatched, not only the version it was sampled on.
	for _, p := range w.failedPats {
		if p.Harness == harness && p.Covers(version) {
			return api.HarnessStateControlFailed, true
		}
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
	if w.suspended != nil && w.suspended() {
		return
	}
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
		delete(w.tx, key)
		return
	}
	// The transcript source reads before the quiet gate and before the did-work
	// clear: a heartbeating seat never opens either.
	if w.visitTranscript(now, &s) {
		return
	}
	// The did-work clear is for a state that describes the screen the seat was
	// looking at. A coverage state describes the version, not the screen, so it
	// stays: a seat that alternates work and quiet would otherwise clear and
	// re-set it every cycle.
	if hs := s.HarnessState; hs != nil && !isCoverageState(hs.State) && hs.Source != api.HarnessSourceTranscript && api.WorkAt(&s).After(hs.ContextAt) {
		w.clear(s, "the session did work")
		return
	}
	adapter := w.reg.Resolve(s.Runtime.Name)
	rule, ok := adapter.(runtime.ForegroundRule)
	if !ok {
		return
	}
	quietSince := api.WorkAt(&s)
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
	case res.State == panestate.StateLoggedOut, res.State == panestate.StateParked:
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
			Kind: events.KindWatchdogUncovered, Severity: events.SeverityWarning,
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
		PaneWidth: width, CapturedAt: now, ContextAt: api.WorkAt(&s),
	})
}

func harnessState(now time.Time, s api.Session, res panestate.Result, harness, version string, width int) *api.HarnessState {
	return &api.HarnessState{
		State: string(res.State), Confidence: string(res.Confidence),
		Harness: harness, HarnessVersion: version,
		PatternID: res.PatternID, PatternVersion: res.PatternVersion, PatternFor: res.HarnessVersion,
		Reason: res.Reason, PaneWidth: width, Evidence: res.Evidence, CapturedAt: now, ContextAt: api.WorkAt(&s),
	}
}

func (w *watchdog) set(now time.Time, s api.Session, res panestate.Result, harness, version string, width int) {
	hs := harnessState(now, s, res, harness, version, width)
	already := s.HarnessState != nil && s.HarnessState.State == hs.State
	w.store.SetHarnessState(s.Key(), hs)
	if already {
		return
	}
	events.Emit(w.ring, events.Event{
		Kind: events.KindSessionHarnessState, Severity: events.SeverityWarning,
		Workspace: s.Workspace, Team: s.Team, Role: s.Role, Session: s.Name,
		Message: fmt.Sprintf("%s %s (%s, %s %s, pattern %s@%d, width %d): %s",
			hs.State, s.Name, res.Confidence, harness, versionOrUnknown(version),
			res.PatternID, res.PatternVersion, width, strings.Join(res.Evidence, " / ")),
	})
}

func versionOrUnknown(v string) string {
	if v == "" {
		return "version unknown"
	}
	return v
}

// clearEvent emits the cleared event when a logged-out or parked state is
// ending.
func (w *watchdog) clearEvent(s api.Session, why string) {
	if s.HarnessState == nil || (s.HarnessState.State != api.HarnessStateLoggedOut && s.HarnessState.State != api.HarnessStateParked) {
		return
	}
	events.Emit(w.ring, events.Event{
		Kind: events.KindSessionHarnessStateCleared, Severity: events.SeverityInfo,
		Workspace: s.Workspace, Team: s.Team, Role: s.Role, Session: s.Name,
		Message: fmt.Sprintf("%s cleared for %s: %s", s.HarnessState.State, s.Name, why),
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
	window := configuredQuietWindow()
	w := watchdogFor(d.store, d.events, sets, window)
	if w == nil {
		return
	}
	w.suspended = d.clusterSuspended
	w.readPane = d.driver.PaneForeground
	w.capture = d.driver.CapturePaneJoined
	d.wireWatchdogClient(w)
	d.watchdog = w
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		w.Run(ctx)
	}()
}

// wireWatchdogClient gives the watchdog the workspace test that keeps the
// transcript reader off a client seat: the workspace's stored scope, the same
// source the COMPOSER column uses.
func (d *Daemon) wireWatchdogClient(w *watchdog) {
	w.client = d.workspaceIsClient
}

// configuredQuietWindow is the operator's watchdog.window from the client
// config, zero when it is unset or unreadable. The watchdog and the controller's
// ACTIVE% reading both start from it, so they judge a seat quiet by one window.
func configuredQuietWindow() time.Duration {
	cfg, err := config.Load()
	if cfg == nil {
		if err != nil {
			log.Printf("watchdog: client config unreadable, using the default window: %v", err)
		}
		return 0
	}
	window, werr := cfg.WatchdogWindow()
	if werr != nil {
		log.Printf("watchdog: %v, using the default", werr)
		return 0
	}
	return window
}

// transcriptTailCap bounds one read of a transcript. The newest assistant
// entry decides, so a longer file is read from its last transcriptTailCap
// bytes and nothing older matters.
const transcriptTailCap = 1 << 20

// readTranscriptTail returns the bytes of path from an offset to the end, at
// most transcriptTailCap of them.
func readTranscriptTail(path string, from int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(f, transcriptTailCap))
}

// The reasons the transcript reader has no reading for a seat. They are
// status on the session, so describe says why a seat is uncovered.
const (
	gapNoSessionID   = "transcript: no assigned session id"
	gapBadSessionID  = "transcript: the session id is not a file name"
	gapNoConfigDir   = "transcript: the config directory is not resolvable"
	gapNoFile        = "transcript: no file for the session id"
	gapSeveralFiles  = "transcript: several files for the session id"
	gapClientNotRead = "transcript: client workspace, not read"
	gapUnreadable    = "transcript: the file cannot be read"
)

// transcriptSource is the watchdog's second reader (docs/design/
// harness-state-watchdog-p1.md section 4a). It reads the typed fields of a
// claude seat's own transcript and reports whether the newest assistant entry
// is an authentication failure. It runs on every tick, outside the quiet gate:
// a seat whose login expired keeps beating, and every beat moves ContextAt
// (#801), so the pane path never gets to look at it.
//
// ok is false when the reader does not apply or cannot read, with the reason
// recorded as the session's TranscriptGap.
func (w *watchdog) transcript(s api.Session) (r panestate.TranscriptReading, ok bool) {
	key := s.Key()
	gap := func(g string) (panestate.TranscriptReading, bool) {
		if s.TranscriptGap != g {
			w.store.SetTranscriptGap(key, g)
		}
		delete(w.tx, key)
		return panestate.TranscriptReading{}, false
	}
	if w.reg.Resolve(s.Runtime.Name).Name() != "claude" {
		return gap("")
	}
	// Until the operator rules on reading client-workspace transcripts, a client
	// workspace's seat is not read, one whose workspace cannot be read counts as
	// client, and so does a watchdog with no client test.
	if w.client == nil {
		return gap(gapClientNotRead)
	}
	if c, err := w.client(s.Workspace); err != nil || c {
		return gap(gapClientNotRead)
	}
	id := s.HarnessSessionID
	switch {
	case id == "":
		return gap(gapNoSessionID)
	case strings.ContainsAny(id, "/\\\x00"):
		return gap(gapBadSessionID)
	}

	t := w.tx[key]
	if t != nil {
		if _, err := os.Stat(t.path); err != nil {
			t = nil
		}
	}
	if t == nil {
		path, why := findTranscript(s, id)
		if why != "" {
			return gap(why)
		}
		t = &transcriptTail{path: path, offset: -1}
		w.tx[key] = t
	}
	fi, err := os.Stat(t.path)
	if err != nil {
		return gap(gapUnreadable)
	}
	if s.TranscriptGap != "" {
		w.store.SetTranscriptGap(key, "")
	}
	if t.offset >= 0 && fi.Size() == t.size && fi.ModTime().Equal(t.mtime) {
		return t.read, true
	}
	from := t.offset
	if from < 0 || fi.Size() < from {
		from = 0
	}
	if fi.Size()-from > transcriptTailCap {
		from = fi.Size() - transcriptTailCap
	}
	data, err := w.tailRead(t.path, from)
	if err != nil {
		return t.read, t.offset >= 0
	}
	t.size, t.mtime = fi.Size(), fi.ModTime()
	// Only whole lines are consumed: a line still being written is read again
	// once it is finished.
	end := bytes.LastIndexByte(data, '\n') + 1
	t.offset = from + int64(end)
	if end > 0 {
		var lines [][]byte
		for _, l := range bytes.Split(data[:end], []byte{'\n'}) {
			if len(l) > 0 {
				lines = append(lines, l)
			}
		}
		if got := panestate.ClassifyClaudeTranscript(lines); got.Verdict != panestate.TranscriptNone {
			t.read = got
		}
	}
	return t.read, true
}

// findTranscript locates the seat's transcript by its assigned id. Globbing by
// the id avoids deriving claude's cwd slug, which is why the codex rollout
// reader refuses to derive its path too. Exactly one match is a file; none or
// several is a reason.
func findTranscript(s api.Session, id string) (path, why string) {
	home, _ := os.UserHomeDir()
	dir := api.CanonicalConfigDir("claude", s.Runtime.Env["CLAUDE_CONFIG_DIR"], home)
	if dir == "" {
		if home == "" {
			return "", gapNoConfigDir
		}
		dir = filepath.Join(home, ".claude")
	}
	if !filepath.IsAbs(dir) {
		return "", gapNoConfigDir
	}
	projects := filepath.Join(dir, "projects")
	entries, err := os.ReadDir(projects)
	if err != nil {
		return "", gapNoFile
	}
	var found []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(projects, e.Name(), id+".jsonl")
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			found = append(found, p)
		}
	}
	switch len(found) {
	case 0:
		return "", gapNoFile
	case 1:
		return found[0], ""
	}
	return "", gapSeveralFiles
}

// visitTranscript runs the transcript source for one seat and reports whether
// a transcript state stands, in which case the pane path has nothing to add. A
// transcript state is cleared only by this reader: when the seat answers, or
// when the file is no longer read. The pane picker's state is the pane
// path's, and a seat that never logged in may write no assistant entry at
// all, so it stands when the transcript has no authentication failure.
func (w *watchdog) visitTranscript(now time.Time, s *api.Session) bool {
	r, ok := w.transcript(*s)
	if ok && r.Verdict == panestate.TranscriptAuthFailed {
		w.setTranscript(now, *s, r)
		return true
	}
	if hs := s.HarnessState; hs != nil && hs.Source == api.HarnessSourceTranscript {
		why := "the transcript is no longer read"
		if ok {
			why = "the session answered"
		}
		w.clear(*s, why)
		s.HarnessState = nil
	}
	return false
}

// clip keeps a transcript field short before it reaches evidence or an event.
func clip(v string) string {
	if r := []rune(v); len(r) > 64 {
		return string(r[:64])
	}
	return v
}

func (w *watchdog) setTranscript(now time.Time, s api.Session, r panestate.TranscriptReading) {
	hs := &api.HarnessState{
		State: api.HarnessStateLoggedOut, Confidence: "high",
		Harness: "claude", HarnessVersion: clip(r.Version),
		PatternID: "transcript:authentication_failed", PatternVersion: 1,
		Evidence:   []string{fmt.Sprintf("entry %s at %s", clip(r.UUID), clip(r.Timestamp))},
		CapturedAt: now, ContextAt: api.WorkAt(&s), Source: api.HarnessSourceTranscript,
	}
	already := s.HarnessState != nil && s.HarnessState.State == hs.State
	w.store.SetHarnessState(s.Key(), hs)
	if already {
		return
	}
	events.Emit(w.ring, events.Event{
		Kind: events.KindSessionHarnessState, Severity: events.SeverityWarning,
		Workspace: s.Workspace, Team: s.Team, Role: s.Role, Session: s.Name,
		Message: fmt.Sprintf("%s %s (high, claude %s, from the transcript's API error, %s)",
			hs.State, s.Name, versionOrUnknown(hs.HarnessVersion), hs.Evidence[0]),
	})
}
