package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
)

// The watch view across clusters (docs/design/multicluster-read-view-plan.md,
// section 3 and #832). Each cluster is polled on its own: one fetch in flight
// per cluster, a timer that frees the slot, and a sequence number so a reply
// older than the last one shown for that cluster is dropped. A cluster that
// does not answer never takes the frame from the others, and each cluster
// shows the time its own rows were read.

// fanWatchCluster is one entry of the set. An entry whose address was already
// dialled, with the same identity, is not polled: repeatOf names the first.
type fanWatchCluster struct {
	name     string
	repeatOf string
}

// fanFetch is what one poll of one cluster brings back.
type fanFetch struct {
	rec         clusterRecord
	rows        []api.Session
	fingerprint string
	stamp       daemonStamp
	stampOK     bool
}

// fanEvent is what the loop did with one event, for a test to order itself by.
type fanEvent struct {
	Kind    string // result, timeout, stale, key, tick
	Cluster string
	Seq     uint64
	Applied bool
}

type fanWatchConfig struct {
	clusters []fanWatchCluster
	interval time.Duration
	stale    time.Duration // no answer this long reads late
	timeout  time.Duration // no answer this long frees the cluster's slot
	// fetch polls one cluster; wantStamp asks it to read the daemon's pid and
	// start time too, which the merge needs when another entry shares its key.
	fetch func(cluster string, seq uint64, wantStamp bool) fanFetch
	// keyOf is daemonKey for a cluster and the fingerprint its dial verified.
	keyOf func(cluster, fingerprint string) string
	now   func() time.Time
	out   func(frame string)
	trace func(fanEvent) // optional
}

// fanState is what the view holds for one cluster.
type fanState struct {
	fanWatchCluster
	rec      clusterRecord // the last result applied, good or not
	rows     []api.Session // the last good rows
	stamp    daemonStamp
	stampOK  bool
	fp       string
	asOf     time.Time // when rows were read
	have     bool      // rows holds a good read
	fetched  bool      // a result or a timeout has been applied
	failed   bool      // the last applied result was not a read
	late     bool      // the fetch in flight is past the stale interval
	inflight bool
	seq      uint64 // the last fetch started
	applied  uint64 // the highest fetch applied, by result or by timeout
	timers   []*time.Timer
}

type fanResult struct {
	cluster string
	seq     uint64
	f       fanFetch
}

type fanTimer struct {
	cluster string
	seq     uint64
	kind    string // stale or timeout
}

// fanWatchLoop draws the watch view across clusters until q or Ctrl-C. Like
// watchLoop, a fetch runs in its own goroutine and the loop never waits on
// one, so keys are always read.
func fanWatchLoop(ws *watchSort, cfg fanWatchConfig, keys <-chan byte, ticks <-chan time.Time) {
	states := make([]*fanState, len(cfg.clusters))
	byName := map[string]*fanState{}
	for i, c := range cfg.clusters {
		states[i] = &fanState{fanWatchCluster: c}
		byName[c.name] = states[i]
	}
	results := make(chan fanResult, 8*len(states)+8)
	timers := make(chan fanTimer, 8*len(states)+8)
	// Closed when the loop returns, so a fetch that finishes later drops its
	// result and ends. The loop does not wait for one: a key must never sit
	// behind a daemon that is not answering.
	stop := make(chan struct{})
	defer close(stop)

	trace := func(e fanEvent) {
		if cfg.trace != nil {
			cfg.trace(e)
		}
	}
	// wantStamp is true when another entry has the same non-empty key.
	wantStamp := func(s *fanState) bool {
		k := cfg.keyOf(s.name, s.fp)
		if k == "" {
			return false
		}
		for _, o := range states {
			if o != s && o.repeatOf == "" && cfg.keyOf(o.name, o.fp) == k {
				return true
			}
		}
		return false
	}
	stopTimers := func(s *fanState) {
		for _, t := range s.timers {
			t.Stop()
		}
		s.timers = nil
	}
	start := func(s *fanState) {
		if s.repeatOf != "" || s.inflight {
			return
		}
		s.seq++
		seq, want := s.seq, wantStamp(s)
		s.inflight, s.late = true, false
		arm := func(d time.Duration, kind string) {
			s.timers = append(s.timers, time.AfterFunc(d, func() {
				select {
				case timers <- fanTimer{s.name, seq, kind}:
				case <-stop:
				}
			}))
		}
		arm(cfg.stale, "stale")
		arm(cfg.timeout, "timeout")
		go func() {
			f := cfg.fetch(s.name, seq, want)
			select {
			case results <- fanResult{s.name, seq, f}:
			case <-stop:
			}
		}()
	}
	draw := func() { cfg.out(renderFanFrame(ws, cfg, states, cfg.now())) }

	for _, s := range states {
		start(s)
	}
	draw()
	for {
		select {
		case key := <-keys:
			quit, redraw := handleWatchKey(ws, key)
			if quit {
				for _, s := range states {
					stopTimers(s)
				}
				cfg.out("\033[2J\033[H")
				trace(fanEvent{Kind: "key", Applied: true})
				return
			}
			if redraw {
				draw()
			}
			trace(fanEvent{Kind: "key", Applied: redraw})
		case <-ticks:
			if !ws.showHelp {
				for _, s := range states {
					start(s)
				}
			}
			trace(fanEvent{Kind: "tick", Applied: true})
		case r := <-results:
			s := byName[r.cluster]
			applied := r.seq > s.applied
			if applied {
				s.applied = r.seq
				if r.seq == s.seq {
					s.inflight, s.late = false, false
					stopTimers(s)
				}
				s.apply(r.f, cfg.now())
				if !ws.showHelp {
					draw()
				}
			}
			trace(fanEvent{Kind: "result", Cluster: r.cluster, Seq: r.seq, Applied: applied})
		case t := <-timers:
			s := byName[t.cluster]
			applied := s.inflight && t.seq == s.seq && t.seq > s.applied
			if applied {
				switch t.kind {
				case "stale":
					s.late = true
				case "timeout":
					s.applied = t.seq
					s.inflight, s.late = false, false
					stopTimers(s)
					s.fail(clusterRecord{
						Cluster: s.name, Outcome: outcomeNoAnswer, Phase: "reply",
						Cause: fmt.Sprintf("no answer within %s", cfg.timeout),
					})
				}
				if !ws.showHelp {
					draw()
				}
			}
			trace(fanEvent{Kind: t.kind, Cluster: t.cluster, Seq: t.seq, Applied: applied})
		}
	}
}

// apply takes a finished fetch into the cluster's state. A fetch that did not
// read keeps the last good rows.
func (s *fanState) apply(f fanFetch, now time.Time) {
	s.fetched = true
	if f.rec.Outcome != outcomeAnswered && f.rec.Outcome != outcomeStarting {
		s.fail(f.rec)
		return
	}
	s.rec, s.failed = f.rec, false
	s.rec.Cluster = s.name
	s.rows = make([]api.Session, len(f.rows))
	for i, r := range f.rows {
		r.Cluster = s.name
		s.rows[i] = r
	}
	s.fp, s.stamp, s.stampOK = f.fingerprint, f.stamp, f.stampOK
	s.asOf, s.have = now, true
}

func (s *fanState) fail(rec clusterRecord) {
	s.fetched, s.failed = true, true
	s.rec = rec
	s.rec.Cluster = s.name
}

// renderFanFrame draws the frame from what the polls have brought back: a
// status line for each cluster, then one table of the rows of every cluster
// that gave any.
func renderFanFrame(ws *watchSort, cfg fanWatchConfig, states []*fanState, now time.Time) string {
	if ws.showHelp {
		return renderWatchFrame(ws, cfg.interval, now)
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "marvel get sessions    %s    (every %v)\n", now.Format("15:04:05"), cfg.interval)
	sortLabel := ws.column
	if sortLabel == "" {
		sortLabel = "workspace, name"
	}
	if ws.desc {
		sortLabel += " desc"
	} else {
		sortLabel += " asc"
	}
	fmt.Fprintf(&buf, "sort: %s    ?:help  q:quit\n\n", sortLabel)

	// What a cluster shows: its last good read, or its failure. Clusters with
	// a good read take part in the same-daemon merge.
	n := len(states)
	records := make([]clusterRecord, n)
	rowSets := make([][]api.Session, n)
	keys := make([]string, n)
	stamps := make([]daemonStamp, n)
	oks := make([]bool, n)
	for i, s := range states {
		records[i] = s.rec
		records[i].Cluster = s.name
		if s.have {
			rowSets[i] = append([]api.Session(nil), s.rows...)
			if !s.failed {
				keys[i] = cfg.keyOf(s.name, s.fp)
				stamps[i], oks[i] = s.stamp, s.stampOK
			}
		}
	}
	foldSameDaemon(keys, stamps, oks, records, rowSets)
	index := map[string]int{}
	for i, s := range states {
		index[s.name] = i
	}
	for i, s := range states {
		if s.repeatOf != "" {
			records[i] = recordForRepeatedAddress(s.name, records[index[s.repeatOf]])
		}
		fmt.Fprintln(&buf, fanStatusLine(s, records[i]))
	}
	buf.WriteString("\n")

	var rows []api.Session
	for i, s := range states {
		if s.repeatOf == "" {
			rows = append(rows, rowSets[i]...)
		}
	}
	ws.lastSessions = rows
	sortSessions(ws.lastSessions, ws)
	fit := fitOptions{
		width:    terminalWidth(),
		explicit: ws.explicit,
		noTrunc:  ws.noTrunc,
		lead:     []sessionColumn{fanOutKeyColumn()},
	}
	buf.WriteString(renderSessionsView(ws.lastSessions, ws.columns, "", fit))
	return buf.String()
}

// fanStatusLine is the line a cluster gets in the frame.
func fanStatusLine(s *fanState, rec clusterRecord) string {
	data := "no data yet"
	if s.have {
		data = "data as of " + s.asOf.Format("15:04:05")
	}
	switch {
	case s.repeatOf != "" || rec.Outcome == outcomeSameDaemon:
		return fmt.Sprintf("cluster %s: %s: %s", s.name, rec.Outcome, drawn(rec.Cause))
	case s.late:
		return fmt.Sprintf("cluster %s: not answering, %s", s.name, data)
	case !s.fetched:
		return fmt.Sprintf("cluster %s: waiting for the first answer", s.name)
	case s.failed:
		return fmt.Sprintf("cluster %s: %s: %s, %s", s.name, rec.Outcome, drawn(rec.Cause), data)
	}
	cnt := len(s.rows)
	if rec.Sessions != nil {
		cnt = *rec.Sessions
	}
	return fmt.Sprintf("cluster %s: %s: %d sessions, as of %s", s.name, rec.Outcome, cnt, s.asOf.Format("15:04:05"))
}

// fanWatchBackstop is added to the cluster timer for the loop's own timer. The
// dial's timer fires first and carries the phase; the loop's frees the slot
// even when a fetch does not return. A variable so a test can shorten it.
var fanWatchBackstop = 2 * time.Second

// newFanWatchConfig is the loop's configuration for a set of resolved
// clusters, without the drawing function, which belongs to the terminal.
func newFanWatchConfig(targets []clusterTarget, params json.RawMessage, interval time.Duration) fanWatchConfig {
	byName := map[string]clusterTarget{}
	clusters := make([]fanWatchCluster, len(targets))
	firstAt := firstAddress(targets)
	for i, t := range targets {
		byName[t.Name] = t
		clusters[i] = fanWatchCluster{name: t.Name}
		if firstAt[i] != i {
			clusters[i].repeatOf = targets[firstAt[i]].Name
		}
	}
	return fanWatchConfig{
		clusters: clusters,
		interval: interval,
		stale:    watchFetchDeadline,
		timeout:  fanOutTimeout + fanWatchBackstop,
		fetch: func(name string, _ uint64, wantStamp bool) fanFetch {
			t := byName[name]
			began := time.Now()
			rec, rows, fp := readCluster(t, params)
			f := fanFetch{rec: rec, rows: rows, fingerprint: fp}
			// The stamp is read inside what is left of the one timer, so a poll
			// never outlives it. A slow daemon loses its stamp for this frame,
			// and the rows it gave still count.
			if left := fanOutTimeout - time.Since(began); wantStamp && rec.answered() && left > 0 {
				f.stamp, f.stampOK = readStampWithin(t, left)
			}
			return f
		},
		keyOf: func(name, fp string) string { return daemonKey(byName[name].Addr, fp) },
		now:   time.Now,
	}
}

// runFanWatch is get sessions -w across a cluster set.
func runFanWatch(names []string, columns string, noTrunc bool, interval time.Duration) error {
	cols, explicit, err := loadSessionColumnsSel(columns)
	if err != nil {
		return err
	}
	cfgFile, err := config.Load()
	if err != nil {
		return fmt.Errorf("the client config is unreadable: %w", err)
	}
	targets := make([]clusterTarget, 0, len(names))
	for _, n := range names {
		t, err := resolveCluster(cfgFile, n)
		if err != nil {
			return err
		}
		targets = append(targets, t)
	}
	params, err := json.Marshal(getRequest(fanOutResource, wantsComposer(cols)))
	if err != nil {
		return err
	}
	return withWatchTerminal(interval, func(keys <-chan byte, ticks <-chan time.Time, out func(string)) {
		ws := newWatchState(cols)
		ws.explicit, ws.noTrunc = explicit, noTrunc
		cfg := newFanWatchConfig(targets, params, interval)
		cfg.out = out
		fanWatchLoop(ws, cfg, keys, ticks)
	})
}

// errNeedTerminal is what a watch says when stdin is not a terminal.
var errNeedTerminal = errors.New("watch mode requires a terminal")
