package usage

import (
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/asof"
	"github.com/arcavenae/marvel/internal/events"
	rtevents "github.com/arcavenae/marvel/internal/runtime/events"
)

// Default compaction-detection hysteresis.
//
// Chosen to be un-fireable by ordinary variation rather than calibrated:
// no live compaction was crossed on any harness, so the bound is reasoned,
// not measured. Within one segment on one model the occupancy series is
// monotone non-decreasing (measured 33377 then 33481 then 34136 while
// input_tokens fell 11368 to 2 to 331), and the one compaction geometry
// on record is roughly 167k down to 96k on a 200k window (orc
// finding-066), a 71k drop. A real compaction therefore clears the guard
// by an order of magnitude, and the threshold only has to reject sampling
// artifacts.
//
// Because the numerator is a level rather than an accumulator, a
// mis-tuned value costs a wrong Compactions count and nothing else.
const (
	defaultHysteresisTokens   = api.CompactionHysteresisTokens
	defaultHysteresisFraction = api.CompactionHysteresisFraction
)

// Coords is the marvel-side identity of an observed session, passed in by
// the caller rather than recovered from the store per sample. The caller
// already holds all four at the tap point, so a store read inside the
// harness back-pressure path buys nothing and fails for a session
// deleted one event earlier.
type Coords struct {
	AgentID   string // == api.Session.Key(), "workspace/name"
	Workspace string
	Team      string
	Role      string
}

// Bind records what marvel knows at launch, before any harness output:
// the harness, the model named in the launch args, and any operator
// window override. Called at spawn so CTX% is resolvable on the FIRST
// fold rather than after a round trip.
type Bind struct {
	Harness string
	Args    []string
	// Model is a launch-arg model, "" when absent.
	Model string
	// Window is a manifest override, 0 when unset.
	Window int
	// Redirection is the spawn-time backend verdict marvel classified for
	// this session (api.ClassifyBackendRedirection over the constructed
	// environment). It rides every denominator resolution for the session
	// so a redirected or unobserved backend is graded, and eventually
	// refused, on the keyed rungs. The zero value is BackendUnknown
	// ("cannot tell").
	Redirection api.BackendRedirection
}

// Sink receives computed readings. *api.Store satisfies it.
type Sink interface {
	UpdateSessionContext(key string, c api.SessionContext)
	// UpdateSessionSpend records spend alone, for a feed that carries spend and
	// no occupancy.
	UpdateSessionSpend(key string, sp api.SessionSpend)
}

// Accountant folds adapter events into per-session context occupancy and
// per-session/per-team spend.
//
// LOCKING: mu guards state, teams, and stats. It is held for map access
// and arithmetic only, and is released before any sink write, event
// emission, log line, or Resolver call. Every method is safe for
// concurrent use, so a future OTEL receiver goroutine can share one
// accountant with N per-session drain goroutines. All methods are
// nil-receiver safe, matching the events.Emit convention, so callers need
// no nil checks.
type Accountant struct {
	mu     sync.Mutex
	state  map[string]*sessionState
	teams  map[string]*teamState
	warned map[string]struct{}
	stats  Stats

	sink   Sink
	limits *Resolver
	ev     events.Emitter
	clock  func() time.Time

	hystAbs  int
	hystFrac float64
}

var _ Reader = (*Accountant)(nil)

// Option configures an Accountant.
type Option func(*Accountant)

// WithEvents wires the control-plane ring so the accountant can report an
// unresolved denominator.
func WithEvents(e events.Emitter) Option {
	return func(a *Accountant) { a.ev = e }
}

// WithClock overrides the accountant's clock.
func WithClock(f func() time.Time) Option {
	return func(a *Accountant) {
		if f != nil {
			a.clock = f
		}
	}
}

// WithCompactionHysteresis overrides the downward-step guard. See the
// package constants for how the defaults were reasoned.
func WithCompactionHysteresis(absTokens int, frac float64) Option {
	return func(a *Accountant) {
		a.hystAbs = absTokens
		a.hystFrac = frac
	}
}

// New builds an accountant writing readings through sink. A nil resolver
// falls back to the shipped table.
func New(sink Sink, limits *Resolver, opts ...Option) *Accountant {
	if limits == nil {
		limits = NewResolver(DefaultTable())
	}
	a := &Accountant{
		state:    make(map[string]*sessionState),
		teams:    make(map[string]*teamState),
		warned:   make(map[string]struct{}),
		sink:     sink,
		limits:   limits,
		clock:    func() time.Time { return time.Now().UTC() },
		hystAbs:  defaultHysteresisTokens,
		hystFrac: defaultHysteresisFraction,
	}
	for _, o := range opts {
		o(a)
	}
	return a
}

// sessionState is one session's accumulation. Occupancy is a level
// (tokens, latest-wins); spend accumulates.
type sessionState struct {
	agentID   string
	workspace string
	team      string
	harness   string
	profile   profile

	// rawModel is the model as the harness names it, and the key into a
	// terminal sample's per-model window declaration. primaryModel is its
	// identity key, used to tell a routing side-request apart from the
	// session's own series.
	rawModel      string
	primaryModel  string
	args          []string
	manifestLimit int
	redirection   api.BackendRedirection

	limit    int
	limitSrc LimitSource

	tokens      int
	peak        float64
	requests    int
	compactions int
	spend       Spend

	// recIn, recCacheRead and recCacheCreation accumulate PRIMARY-model
	// classes only, for the end-of-session reconciliation. Separate from
	// spend because a terminal total covers the primary model alone (the
	// fixture's result.usage excludes the routing model's 527 tokens), so
	// comparing total spend against it would mismatch on any session that
	// routed.
	recIn            int
	recCacheRead     int
	recCacheCreation int

	firstAt time.Time
	lastAt  time.Time

	// rateSum is the exponentially decayed count of output tokens as of
	// rateAt; rateAt is zero until the first sample.
	rateSum float64
	rateAt  time.Time

	// started is set once a session-start event has been seen for this
	// session under this accountant. Without it, the session was already
	// running when the accountant began (a daemon restart or reexec), so a
	// cumulative feed's first total is output from before this accountant
	// existed.
	started bool
}

type teamState struct {
	retired      Spend
	retiredCount int
	partial      bool
	since        time.Time
}

// foldResult is what a fold decided to do after the lock is released.
type foldResult struct {
	write   bool
	reading api.SessionContext
	// spend, when set, is a spend-only update for a feed with no occupancy.
	spend       *api.SessionSpend
	unresolved  bool
	learnModel  string
	learnWindow int
	// learnVerdict is the session's backend verdict, so a learned window is
	// cached under the backend it was measured on (D-key, aae-orc-bv7m).
	learnVerdict api.BackendRedirection
	warnings     []string
}

// Bind records launch-time knowledge for a session and resolves its
// denominator eagerly.
func (a *Accountant) Bind(c Coords, b Bind) {
	if a == nil {
		return
	}
	prof, _ := profileFor(b.Harness)
	limit, src, model := a.limits.Resolve(Request{
		Harness:       b.Harness,
		StreamModel:   b.Model,
		RuntimeArgs:   b.Args,
		ManifestLimit: b.Window,
		Redirection:   b.Redirection,
	})

	a.mu.Lock()
	defer a.mu.Unlock()
	a.state[c.AgentID] = &sessionState{
		agentID:       c.AgentID,
		workspace:     c.Workspace,
		team:          c.Team,
		harness:       b.Harness,
		profile:       prof,
		rawModel:      model,
		primaryModel:  IdentityKey(model),
		args:          b.Args,
		manifestLimit: b.Window,
		redirection:   b.Redirection,
		limit:         limit,
		limitSrc:      src,
	}
}

// Observe folds one adapter event and writes the resulting reading
// through the sink.
//
// CONTRACT: called from the session manager's per-session drain
// goroutine, which is the only consumer of a buffered channel that
// back-pressures the harness by design. Observe must therefore do map
// lookups plus arithmetic under its own mutex and one non-persisting
// store write. No file I/O, no network, no blocking, no persist.
func (a *Accountant) Observe(c Coords, ev rtevents.Event) {
	if a == nil {
		return
	}
	prof, known := profileFor(ev.Harness)
	if !known {
		a.mu.Lock()
		a.stats.SamplesIgnored++
		a.mu.Unlock()
		return
	}
	if ev.Event == rtevents.KindSessionStarted {
		a.observeStart(c, ev, prof)
		return
	}

	res := a.fold(c, ev, prof)

	for _, w := range res.warnings {
		log.Print(w)
	}
	if res.learnWindow > 0 {
		a.limits.Learn(res.learnModel, res.learnWindow, res.learnVerdict)
	}
	if res.unresolved {
		events.Emit(a.ev, events.Event{
			Kind:      events.KindContextLimitUnresolved,
			Severity:  events.SeverityWarning,
			Workspace: c.Workspace,
			Team:      c.Team,
			Role:      c.Role,
			Session:   c.AgentID,
			Message: fmt.Sprintf("context tokens measured but no window resolved for model %q on %s; CTX%% stays blank",
				orNone(res.reading.ContextModel), ev.Harness),
		})
	}
	if res.write && a.sink != nil {
		a.sink.UpdateSessionContext(c.AgentID, res.reading)
	}
	if res.spend != nil && a.sink != nil {
		a.sink.UpdateSessionSpend(c.AgentID, *res.spend)
	}
}

// observeStart captures the model a harness names at session start and
// re-resolves the denominator against it. Claude is the only harness that
// names one; codex and opencode leave the launch args as the only key.
func (a *Accountant) observeStart(c Coords, ev rtevents.Event, prof profile) {
	// Note the start before the model check: codex and opencode name no
	// model, and the start is what the rate needs to know about them.
	a.mu.Lock()
	a.stateLocked(c, ev.Harness, prof).started = true
	a.mu.Unlock()

	d, ok := ev.Data.(rtevents.SessionStartedData)
	if !ok || d.Model == "" {
		return
	}

	a.mu.Lock()
	st := a.stateLocked(c, ev.Harness, prof)
	args, manifest, redirection := st.args, st.manifestLimit, st.redirection
	a.mu.Unlock()

	limit, src, model := a.limits.Resolve(Request{
		Harness:       ev.Harness,
		StreamModel:   d.Model,
		RuntimeArgs:   args,
		ManifestLimit: manifest,
		Redirection:   redirection,
	})

	a.mu.Lock()
	defer a.mu.Unlock()
	st = a.stateLocked(c, ev.Harness, prof)
	st.rawModel = model
	st.primaryModel = IdentityKey(model)
	st.limit = limit
	st.limitSrc = src
}

// fold is the whole arithmetic, under one critical section. Order matters:
// terminal and cumulative samples leave before any occupancy work, and a
// subagent turn or a non-primary model leaves before the compaction
// detector, whose input would otherwise be a 33k collapse every time a
// routing model or a Task tool answered.
func (a *Accountant) fold(c Coords, ev rtevents.Event, prof profile) foldResult {
	var res foldResult

	a.mu.Lock()
	defer a.mu.Unlock()

	st := a.stateLocked(c, ev.Harness, prof)
	s, ok := sampleFromEvent(c, ev, prof, st.rawModel)
	if !ok {
		a.stats.SamplesIgnored++
		return res
	}

	if s.Terminal {
		a.foldTerminalLocked(st, s, &res)
		return res
	}

	// A feed reporting RUNNING TOTALS cannot produce an occupancy level,
	// so it produces none. This is the same defect as reading Claude's
	// terminal result line as a level (see doc.go), arriving through a
	// different door: here every sample is a total, not just the last one,
	// so there is no terminal marker to catch it.
	//
	// Codex is the measured case. `codex exec --json` turn.completed
	// reports the session's accumulated usage. In this repo's own
	// tool_call.jsonl fixture it says input_tokens 28110, while the
	// harness's per-request record for that same thread id says the two
	// requests were 14005 then 14105: the true prompt at turn end was
	// 14105, and 28110 is their sum. The overstatement is 1.99x on a
	// two-request turn and grows with request count.
	//
	// The tokens are real money, so they are recorded as spend by
	// REPLACEMENT rather than accumulation, and the occupancy level is
	// left untouched so CTX% renders absence.
	if s.Cumulation == CumulationSession {
		a.stats.CumulativeSamples++
		a.setSpendLocked(st, s)
		// The tokens are real spend, so the session shows them; the occupancy
		// fields and their source stay as they were, which is absent.
		sp := st.spendReading()
		res.spend = &sp
		if a.warnOnceLocked("cumulative:" + s.Harness) {
			res.warnings = append(res.warnings, fmt.Sprintf(
				"harness %s: per-turn usage is a running session total, not a per-request level, so its tokens count toward spend and CTX%% is reported absent; occupancy needs that harness's own per-request record",
				s.Harness,
			))
		}
		return res
	}

	// A subagent runs its own window, and its prompt is typically a small
	// fraction of the parent's. Folding it in would drop the level for the
	// duration of the tool call, book a compaction that did not happen,
	// and hand a shift trigger a flapping series. Its tokens are real
	// money, so they stay in spend, and they stay in the reconciliation
	// accumulator too: the terminal totals appear to cover every request
	// in the session, so excluding them there would report a permanent
	// mismatch on any session that spawned one. UNVERIFIED, no captured
	// fixture carries a subagent turn; ReconcileMismatches is the counter
	// that will say so if this is wrong.
	if s.Subagent {
		a.stats.SubagentSamples++
		a.addSpendLocked(st, s)
		st.recIn += s.In
		st.recCacheRead += s.CacheReadIn
		st.recCacheCreation += s.CacheCreationIn
		return res
	}

	// A session whose model was never named adopts the first model a
	// sample reports, so codex and opencode (which name none anywhere)
	// keep an empty primary and never route their own samples away.
	if st.primaryModel == "" && s.Model != "" {
		st.rawModel = s.Model
		st.primaryModel = IdentityKey(s.Model)
	}

	if s.Model != "" && st.primaryModel != "" && IdentityKey(s.Model) != st.primaryModel {
		a.stats.NonPrimarySamples++
		a.addSpendLocked(st, s)
		if a.warnOnceLocked("nonprimary:" + c.AgentID) {
			res.warnings = append(res.warnings, fmt.Sprintf(
				"session %s: model %q is not the session's primary %q; its tokens count toward spend but not context occupancy",
				c.AgentID, s.Model, st.rawModel,
			))
		}
		return res
	}

	if m := s.TotalMismatch(); m != 0 {
		a.stats.TotalMismatches++
		if a.warnOnceLocked("total:" + s.Harness) {
			res.warnings = append(res.warnings, fmt.Sprintf(
				"harness %s: token classes do not add up to its own total, off by %d; its usage schema changed or marvel misreads a field",
				s.Harness, m,
			))
		}
	}
	if s.AdditiveConfirmed() {
		a.stats.AdditiveConfirmations++
	}

	occ := s.Occupancy()
	if st.requests > 0 {
		if api.CompactionDroppedBy(st.tokens, occ, a.hystAbs, a.hystFrac) {
			st.compactions++
			a.stats.CompactionsDetected++
		}
	}

	st.tokens = occ
	st.requests++
	a.stats.SamplesObserved++
	a.addSpendLocked(st, s)
	st.recIn += s.In
	st.recCacheRead += s.CacheReadIn
	st.recCacheCreation += s.CacheCreationIn

	now := a.clock()
	if st.firstAt.IsZero() {
		st.firstAt = now
	}
	st.lastAt = now

	// A sample may itself declare the window (a future OTEL feed does;
	// no stream feed does today).
	if s.DeclaredLimit > 0 && st.limit != s.DeclaredLimit {
		st.limit = s.DeclaredLimit
		st.limitSrc = LimitFromStream
		res.learnModel, res.learnWindow, res.learnVerdict = st.rawModel, s.DeclaredLimit, st.redirection
	}

	if st.limit > 0 {
		pct := 100 * float64(st.tokens) / float64(st.limit)
		if pct > st.peak {
			st.peak = pct
		}
	} else if a.warnOnceLocked("unresolved:" + c.AgentID) {
		res.unresolved = true
	}

	res.write = true
	res.reading = st.reading()
	return res
}

// foldTerminalLocked handles a session-end sample. It NEVER touches the
// occupancy level: a terminal sample carries session totals, and folding
// them in is exactly the defect this package is built to prevent. It
// reconciles, learns the declared window, and settles cost.
func (a *Accountant) foldTerminalLocked(st *sessionState, s Sample, res *foldResult) {
	if s.DeclaredLimit > 0 {
		st.limit = s.DeclaredLimit
		st.limitSrc = LimitFromStream
		res.learnModel, res.learnWindow, res.learnVerdict = st.rawModel, s.DeclaredLimit, st.redirection
		if st.tokens > 0 {
			if pct := 100 * float64(st.tokens) / float64(st.limit); pct > st.peak {
				st.peak = pct
			}
			res.write = true
			res.reading = st.reading()
		}
	}

	// Claude reports one cost for the whole session and none per request;
	// opencode reports per request and has no terminal. So a terminal cost
	// replaces an unreported one and never doubles a reported one.
	if s.CostUSD != nil && !st.spend.CostReported {
		st.spend.CostUSD = *s.CostUSD
		st.spend.CostReported = true
	}

	if st.requests == 0 {
		return
	}

	// The invariant: accumulated per-request classes must equal the
	// harness's own totals. Proven exact on
	// internal/runtime/claudecode/testdata/tool_call.ndjson (11701 /
	// 72131 / 17162 either way).
	observed := st.recIn + st.recCacheRead + st.recCacheCreation
	terminal := s.ClassSum()
	switch {
	case observed > terminal:
		// Strictly greater is an arithmetic contradiction: more input
		// tokens counted than the harness says the session used. It is
		// what a cumulative series produces when read as levels.
		a.stats.CumulationViolations++
		res.warnings = append(res.warnings, fmt.Sprintf(
			"session %s: accumulated %d prompt tokens against a harness total of %d; %s per-request usage is cumulative, not a level",
			st.agentID, observed, terminal, s.Harness,
		))
	case observed != terminal:
		a.stats.ReconcileMismatches++
		res.warnings = append(res.warnings, fmt.Sprintf(
			"session %s: accumulated %d prompt tokens against a harness total of %d; a sample was missed or double-counted",
			st.agentID, observed, terminal,
		))
	}
}

func (a *Accountant) addSpendLocked(st *sessionState, s Sample) {
	now := a.clock()
	st.noteOutLocked(s.Out, now)
	st.spend.In += s.In
	st.spend.Out += s.Out
	st.spend.CacheReadIn += s.CacheReadIn
	st.spend.CacheCreationIn += s.CacheCreationIn
	st.spend.ReasoningOut += s.ReasoningOut
	// Sample.Occupancy applies the feed's Layout, so this is the one prompt
	// figure that is layout-independent once accumulated. It is defined only
	// on a non-terminal sample; every call site here sits after fold's
	// terminal early-return, and foldTerminalLocked adds no tokens of its
	// own, so nothing is missed and nothing terminal is folded.
	st.spend.PromptTokens += s.Occupancy()
	st.spend.Requests++
	if s.CostUSD != nil {
		st.spend.CostUSD += *s.CostUSD
		st.spend.CostReported = true
	}
	st.spend.ObservedAt = now
}

// setSpendLocked records a sample whose classes are running session
// totals: the new total REPLACES the previous one. Accumulating them
// would square the count, which is the spend-column form of the same
// mistake that reading a cumulative series as a level is in the occupancy
// column. Requests counts observations, so it still increments; for a
// cumulative feed that is turns rather than requests.
func (a *Accountant) setSpendLocked(st *sessionState, s Sample) {
	now := a.clock()
	// The running total replaced here is not new output: only the part
	// above the previous total is. The first total of a session this
	// accountant did not see start is a baseline, not a burst: the session
	// was already running (a daemon restart or reexec), and that total is
	// output from before there was an accountant to rate it.
	delta := s.Out - st.spend.Out
	if st.spend.Requests == 0 && !st.started {
		delta = 0
	}
	st.noteOutLocked(delta, now)
	st.spend.In = s.In
	st.spend.Out = s.Out
	st.spend.CacheReadIn = s.CacheReadIn
	st.spend.CacheCreationIn = s.CacheCreationIn
	st.spend.ReasoningOut = s.ReasoningOut
	st.spend.PromptTokens = s.Occupancy()
	st.spend.Requests++
	if s.CostUSD != nil {
		st.spend.CostUSD = *s.CostUSD
		st.spend.CostReported = true
	}
	st.spend.ObservedAt = now
}

func (a *Accountant) hysteresis(tokens int) int {
	return api.CompactionBand(tokens, a.hystAbs, a.hystFrac)
}

// stateLocked returns the session's state, creating it if Bind never ran
// (a session adopted mid-stream, or an Observe that beat its Bind).
func (a *Accountant) stateLocked(c Coords, harness string, prof profile) *sessionState {
	st, ok := a.state[c.AgentID]
	if ok {
		return st
	}
	st = &sessionState{
		agentID:   c.AgentID,
		workspace: c.Workspace,
		team:      c.Team,
		harness:   harness,
		profile:   prof,
		limitSrc:  LimitUnresolved,
	}
	a.state[c.AgentID] = st
	return st
}

func (a *Accountant) warnOnceLocked(key string) bool {
	if _, seen := a.warned[key]; seen {
		return false
	}
	a.warned[key] = struct{}{}
	return true
}

// Forget drops a session's state, rolling its spend into its team's
// retired total so a fan-out's accounting survives the sessions that
// produced it. Idempotent: an adopted pane never had state.
func (a *Accountant) Forget(agentID string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.forgetLocked(agentID)
}

func (a *Accountant) forgetLocked(agentID string) {
	st, ok := a.state[agentID]
	if !ok {
		return
	}
	delete(a.state, agentID)
	delete(a.warned, "unresolved:"+agentID)
	delete(a.warned, "nonprimary:"+agentID)

	key := st.workspace + "/" + st.team
	ts, ok := a.teams[key]
	if !ok {
		ts = &teamState{since: a.clock()}
		a.teams[key] = ts
	}
	ts.retiredCount++
	ts.retired.In += st.spend.In
	ts.retired.Out += st.spend.Out
	ts.retired.CacheReadIn += st.spend.CacheReadIn
	ts.retired.CacheCreationIn += st.spend.CacheCreationIn
	ts.retired.ReasoningOut += st.spend.ReasoningOut
	ts.retired.PromptTokens += st.spend.PromptTokens
	ts.retired.CostUSD += st.spend.CostUSD
	ts.retired.Requests += st.spend.Requests
	if st.spend.CostReported {
		ts.retired.CostReported = true
	}
	if st.requests == 0 {
		ts.partial = true
	}
}

// Sweep drops state for keys the store no longer has. Bounds memory
// against a missed Forget and keeps Stats().Tracked honest as the leak
// canary.
func (a *Accountant) Sweep(live map[string]bool) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for key := range a.state {
		if !live[key] {
			a.forgetLocked(key)
		}
	}
}

// Stats returns a snapshot of the diagnostic counters.
func (a *Accountant) Stats() Stats {
	if a == nil {
		return Stats{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.stats
	out.Tracked = len(a.state)
	out.SessionsUnresolved = 0
	for _, st := range a.state {
		if st.requests > 0 && st.limit <= 0 {
			out.SessionsUnresolved++
		}
	}
	return out
}

// SessionOccupancy returns one session's context accounting.
func (a *Accountant) SessionOccupancy(agentID string) (Occupancy, bool) {
	if a == nil {
		return Occupancy{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	st, ok := a.state[agentID]
	if !ok {
		return Occupancy{}, false
	}
	out := Occupancy{
		Tokens:      st.tokens,
		Limit:       st.limit,
		LimitSource: st.limitSrc,
		Model:       st.rawModel,
		Requests:    st.requests,
		Compactions: st.compactions,
		Peak:        st.peak,
		FirstAt:     st.firstAt,
		ObservedAt:  st.lastAt,
	}
	if st.limit > 0 {
		out.Percent = 100 * float64(st.tokens) / float64(st.limit)
	}
	return out, true
}

// OutRateHalfLife is how fast the output-token rate forgets: a stream that
// stops loses half its rate every OutRateHalfLife. One constant, so the
// design owner can move it in one place.
const OutRateHalfLife = 20 * time.Second

// OutRateValidFor is how long a rate reading stays current after the sample
// that produced it. It is the default quiet window, one constant, so a
// stopped stream reads as a rate decaying toward zero for that long and then
// as expired, not as a frozen last value.
const OutRateValidFor = api.DefaultQuietWindow

// OutRate returns the session's output-token rate, in tokens per second,
// decayed to now. The second result is false when the session has never
// been sampled, which is not a rate of zero.
func (a *Accountant) OutRate(agentID string, now time.Time) (float64, bool) {
	if a == nil {
		return 0, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	st, ok := a.state[agentID]
	if !ok || st.rateAt.IsZero() {
		return 0, false
	}
	return rateOf(st.rateSum * decayFactor(now.Sub(st.rateAt))), true
}

// DecayedRate is a rate cell's value decayed from its observation to now.
// A renderer calls it so a stopped stream reads as a falling number between
// readings instead of its last value.
//
// It returns 0 for a cell that was never sampled, the same figure as a
// measured quiet stream, so the renderer must check the cell's ObservedAt
// first and render never-sampled as absent.
func DecayedRate(c asof.Cell[float64], now time.Time) float64 {
	if c.ObservedAt.IsZero() {
		return 0
	}
	return c.Value * decayFactor(now.Sub(c.ObservedAt))
}

// decayFactor is the share of a decayed count that survives dt. A dt at or
// below zero (a clock that stepped back) decays nothing.
func decayFactor(dt time.Duration) float64 {
	if dt <= 0 {
		return 1
	}
	return math.Exp2(-dt.Seconds() / OutRateHalfLife.Seconds())
}

// rateOf turns a decayed token count into tokens per second. A steady
// stream of r tokens a second settles at a count of r*halfLife/ln2.
func rateOf(decayed float64) float64 {
	return decayed * math.Ln2 / OutRateHalfLife.Seconds()
}

// noteOutLocked folds out new output tokens into the session's decayed
// count at now. The count is an exponentially decayed sum, not the delta of
// the last two samples: two large samples a millisecond apart add to the
// count and do not divide by a millisecond.
func (st *sessionState) noteOutLocked(out int, now time.Time) {
	if !st.rateAt.IsZero() {
		st.rateSum *= decayFactor(now.Sub(st.rateAt))
		if now.Before(st.rateAt) {
			now = st.rateAt
		}
	}
	st.rateSum += float64(max(out, 0))
	st.rateAt = now
}

// SessionSpend returns one session's cumulative token and cost spend.
func (a *Accountant) SessionSpend(agentID string) (Spend, bool) {
	if a == nil {
		return Spend{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	st, ok := a.state[agentID]
	if !ok {
		return Spend{}, false
	}
	return st.spend, true
}

// TeamSpend sums a team's live and retired sessions.
//
// Retired spend is accumulated at Forget rather than summed over live
// sessions on demand, because a fan-out's spend is mostly in sessions
// that have already exited and the store deletes their records.
func (a *Accountant) TeamSpend(workspace, team string) TeamTotals {
	if a == nil {
		return TeamTotals{}
	}
	key := workspace + "/" + team

	a.mu.Lock()
	defer a.mu.Unlock()

	var out TeamTotals
	if ts, ok := a.teams[key]; ok {
		out.Spend = ts.retired
		out.EndedSessions = ts.retiredCount
		out.Partial = ts.partial
		out.Since = ts.since
	}
	for _, st := range a.state {
		if st.workspace != workspace || st.team != team {
			continue
		}
		out.LiveSessions++
		out.In += st.spend.In
		out.Out += st.spend.Out
		out.CacheReadIn += st.spend.CacheReadIn
		out.CacheCreationIn += st.spend.CacheCreationIn
		out.ReasoningOut += st.spend.ReasoningOut
		out.PromptTokens += st.spend.PromptTokens
		out.CostUSD += st.spend.CostUSD
		out.Requests += st.spend.Requests
		if st.spend.CostReported {
			out.CostReported = true
		}
		if st.requests == 0 {
			out.Partial = true
		}
		if st.spend.ObservedAt.After(out.ObservedAt) {
			out.ObservedAt = st.spend.ObservedAt
		}
	}
	return out
}

func (st *sessionState) reading() api.SessionContext {
	out := api.SessionContext{
		ContextSource:      api.ContextSourceAccountant,
		ContextTokens:      st.tokens,
		ContextLimit:       st.limit,
		ContextLimitSource: string(st.limitSrc),
		ContextModel:       st.rawModel,
		ContextRequests:    st.requests,
		ContextCompactions: st.compactions,
		ContextPeak:        st.peak,
	}
	if st.limit > 0 {
		out.ContextPercent = 100 * float64(st.tokens) / float64(st.limit)
	}
	// Spend rides the same record so a reader needs no second call. It is
	// set only once a request has been folded: a session that never spent
	// stays absent rather than reading as zero (api.SessionContext).
	sp := st.spendReading()
	out.SpendOut, out.SpendPromptTokens, out.OutRate = sp.Out, sp.PromptTokens, sp.OutRate
	return out
}

// spendReading is the spend half of a reading: cumulative output and prompt
// tokens, once a request has been folded, and the output-token rate once it has
// been sampled.
func (st *sessionState) spendReading() api.SessionSpend {
	var out api.SessionSpend
	if st.spend.Requests > 0 {
		spendOut, spendPrompt := st.spend.Out, st.spend.PromptTokens
		out.Out, out.PromptTokens = &spendOut, &spendPrompt
	}
	if !st.rateAt.IsZero() {
		out.OutRate = asof.Cell[float64]{
			Value:      rateOf(st.rateSum),
			ObservedAt: st.rateAt,
			ValidUntil: st.rateAt.Add(OutRateValidFor),
		}
	}
	return out
}

func orNone(s string) string {
	if s == "" {
		return "(unnamed)"
	}
	return s
}

// QuietRate is a rate cell as a renderer shows it at now: the value, and the
// state that says how to print it. A seat quiet by the shared predicate
// (api.Quiet over window) with an activity channel attached reads exactly 0,
// fresh, instead of a number still decaying toward it, and that rule is
// checked before expiry: the expired ? is for a reading whose channel is
// gone. Without a channel, quiet proves nothing, so the cell's own expiry
// decides. A cell never sampled is absent, not zero.
func QuietRate(c asof.Cell[float64], s *api.Session, window time.Duration, channel bool, now time.Time) (float64, asof.State) {
	if c.State(now) == asof.None {
		return 0, asof.None
	}
	if channel && api.Quiet(s, window, now) {
		return 0, asof.Fresh
	}
	if c.State(now) == asof.Stale {
		return 0, asof.Stale
	}
	return DecayedRate(c, now), asof.Fresh
}
