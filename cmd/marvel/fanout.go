package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/knownhosts"
)

// The multi-cluster read view (docs/design/multicluster-read-view-plan.md,
// sections 2, 3 and 3.1 to 3.2). `get sessions --cluster a,b` and
// `get sessions --all-clusters` read every named cluster in parallel, each
// under one timer, and print one status line per cluster. Every write still
// names one cluster.

// fanOutTimeout is the one timer over connect, handshake, auth, request and
// reply for each cluster of a fan-out (Q1: set here, not on single-cluster
// commands). A variable so a test can shorten it.
var fanOutTimeout = 10 * time.Second

// fanOutMethods are the daemon methods a fan-out may call: reads only.
var fanOutMethods = []string{"get"}

// outcome is how one cluster's read ended. The set is closed and pinned by a
// test.
type outcome string

const (
	outcomeAnswered       outcome = "answered"
	outcomeStarting       outcome = "starting"
	outcomeSameDaemon     outcome = "same_daemon" // set by the merge in #831; nothing here emits it
	outcomeUnreachable    outcome = "unreachable"
	outcomeNoAnswer       outcome = "no_answer_in_time"
	outcomeAuthRefused    outcome = "auth_refused"
	outcomeHostKeyUnknown outcome = "host_key_unknown"
	outcomeHostKeyChanged outcome = "host_key_changed"
	outcomeDaemonError    outcome = "daemon_error"
	outcomeBadReply       outcome = "bad_reply"
)

var allOutcomes = []outcome{
	outcomeAnswered, outcomeStarting, outcomeSameDaemon, outcomeUnreachable, outcomeNoAnswer,
	outcomeAuthRefused, outcomeHostKeyUnknown, outcomeHostKeyChanged, outcomeDaemonError, outcomeBadReply,
}

// outcomeExit is the exit code a cluster that ended this way gives when the
// whole set ended the same way (plan section 3.2). 0 is a cluster that
// answered.
var outcomeExit = map[outcome]int{
	outcomeAnswered:       0,
	outcomeStarting:       0,
	outcomeSameDaemon:     0,
	outcomeDaemonError:    exitBadReply,
	outcomeBadReply:       exitBadReply,
	outcomeUnreachable:    exitUnreachable,
	outcomeNoAnswer:       exitTimeout,
	outcomeAuthRefused:    exitAuthRefused,
	outcomeHostKeyUnknown: exitHostKeyError,
	outcomeHostKeyChanged: exitHostKeyError,
}

const (
	exitPartial    = 10
	exitNoneMixed  = 11
	fanOutResource = "sessions"
)

// clusterRecord is the record for one cluster of a fan-out.
type clusterRecord struct {
	Cluster      string  `json:"cluster"`
	Outcome      outcome `json:"outcome"`
	Phase        string  `json:"phase,omitempty"`
	Fingerprint  string  `json:"fingerprint,omitempty"`
	Cause        string  `json:"cause,omitempty"`
	Next         string  `json:"next,omitempty"`
	AsOf         string  `json:"as_of"`
	Sessions     *int    `json:"sessions,omitempty"`
	SameDaemonAs string  `json:"same_daemon_as,omitempty"`
}

// answered reports whether the cluster gave rows: it answered, or it is
// starting and its rows may be incomplete.
func (r clusterRecord) answered() bool {
	return r.Outcome == outcomeAnswered || r.Outcome == outcomeStarting || r.Outcome == outcomeSameDaemon
}

// statusLine is the one stderr line a cluster gets. The cause may hold the
// daemon's text, so it is filtered.
func (r clusterRecord) statusLine() string {
	line := fmt.Sprintf("marvel: cluster %s: %s: %s", r.Cluster, r.Outcome, drawn(r.Cause))
	if r.Next != "" {
		line += "; " + r.Next
	}
	return line
}

// classifyDialError sorts a failed call into an outcome by the type of the
// error, never by its text. phase is set for a timeout, fingerprint for a host
// key outcome. An error of no known type is reported as unreachable with its
// text as the cause, since nothing answered.
func classifyDialError(err error) (out outcome, phase, fingerprint, cause string) {
	var (
		unk  *knownhosts.HostKeyUnknownError
		chg  *knownhosts.HostKeyChangedError
		ref  *daemonrpc.AuthRefusedError
		noID *daemonrpc.NoIdentityError
		tmo  *daemonrpc.TimeoutError
		bad  *daemonrpc.BadReplyError
		unr  *daemonrpc.UnreachableError
	)
	cause = err.Error()
	switch {
	case errors.As(err, &chg):
		return outcomeHostKeyChanged, "", chg.Fingerprint, cause
	case errors.As(err, &unk):
		return outcomeHostKeyUnknown, "", unk.Fingerprint, cause
	case errors.As(err, &ref), errors.As(err, &noID):
		return outcomeAuthRefused, "", "", cause
	case errors.As(err, &tmo):
		return outcomeNoAnswer, tmo.Phase, "", fmt.Sprintf("no answer within %s in the %s phase", fanOutTimeout, tmo.Phase)
	case errors.As(err, &bad):
		return outcomeBadReply, "", "", cause
	case errors.As(err, &unr):
		return outcomeUnreachable, "", "", cause
	}
	return outcomeUnreachable, "", "", cause
}

// nextStep is the one-clause hint that closes a status line.
func nextStep(o outcome) string {
	switch o {
	case outcomeUnreachable:
		return "check that the daemon runs and the address is right"
	case outcomeNoAnswer:
		return "try again; the host may be busy or asleep"
	case outcomeAuthRefused:
		return "the daemon accepted none of the key offered; check the entry's identity and 'marvel keys authorize'"
	case outcomeHostKeyUnknown:
		return "verify the fingerprint, then run 'marvel keys trust'"
	case outcomeHostKeyChanged:
		return "the host key differs from the recorded one; do not trust it until the change is explained"
	case outcomeDaemonError, outcomeBadReply:
		return "the daemon answered but the answer is unusable; read 'marvel daemon logs' on that cluster"
	case outcomeStarting:
		return "the daemon is still starting; rows may be incomplete"
	}
	return ""
}

// readCluster reads one cluster's sessions under the timer.
func readCluster(t clusterTarget, params json.RawMessage) (clusterRecord, []api.Session) {
	rec := clusterRecord{Cluster: t.Name}
	finish := func() (clusterRecord, []api.Session) {
		rec.AsOf = time.Now().UTC().Format(time.RFC3339)
		if rec.Outcome != outcomeAnswered && rec.Outcome != outcomeStarting && rec.Next == "" {
			rec.Next = nextStep(rec.Outcome)
		}
		return rec, nil
	}
	opts := t.Opts
	opts.StrictHostKey = true
	opts.OfferOneKey = true
	opts.Timeout = fanOutTimeout
	resp, err := daemonrpc.SendRequestWith(t.Addr, daemonrpc.Request{Method: fanOutMethods[0], Params: params}, opts)
	if err != nil {
		rec.Outcome, rec.Phase, rec.Fingerprint, rec.Cause = classifyDialError(err)
		var noID *daemonrpc.NoIdentityError
		if errors.As(err, &noID) {
			rec.Next = "no key could be loaded, so no dial was made; give the entry an identity or run 'marvel keys generate'"
		}
		return finish()
	}
	if resp.Error != "" {
		rec.Cause = resp.Error
		rec.Outcome = outcomeDaemonError
		if resp.Starting {
			rec.Outcome = outcomeStarting
		}
		return finish()
	}
	var rows []api.Session
	if err := json.Unmarshal(resp.Result, &rows); err != nil {
		rec.Outcome, rec.Cause = outcomeBadReply, "the reply did not parse as a list of sessions"
		return finish()
	}
	for i := range rows {
		rows[i].Cluster = t.Name
	}
	rec.Outcome = outcomeAnswered
	if resp.Starting {
		rec.Outcome = outcomeStarting
	}
	n := len(rows)
	rec.Sessions = &n
	rec.Cause = fmt.Sprintf("%d sessions", n)
	rec.AsOf = time.Now().UTC().Format(time.RFC3339)
	rec.Next = nextStep(rec.Outcome)
	return rec, rows
}

// collectFanOut resolves every named cluster from its own config entry, then
// reads them all in parallel. A name that resolves to nothing is an error
// before any dial. Records come back in the order of names.
func collectFanOut(names []string, composer bool) ([]clusterRecord, []api.Session, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("the client config is unreadable: %w", err)
	}
	targets := make([]clusterTarget, 0, len(names))
	for _, n := range names {
		t, err := resolveCluster(cfg, n)
		if err != nil {
			return nil, nil, err
		}
		targets = append(targets, t)
	}
	params, err := json.Marshal(getRequest(fanOutResource, composer))
	if err != nil {
		return nil, nil, err
	}
	records := make([]clusterRecord, len(targets))
	rowSets := make([][]api.Session, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			records[i], rowSets[i] = readCluster(t, params)
		}()
	}
	wg.Wait()
	var rows []api.Session
	for _, rs := range rowSets {
		rows = append(rows, rs...)
	}
	return records, rows, nil
}

// fanOutExit is the exit code of a set (plan section 3.2): 0 when every
// cluster answered; 10 when some answered and some did not, or when any is
// starting; the failures' own code when none answered for one reason; 11 when
// none answered and the reasons differ.
func fanOutExit(records []clusterRecord) int {
	var anyAnswered, anyStarting bool
	codes := map[int]bool{}
	for _, r := range records {
		if r.Outcome == outcomeStarting {
			anyStarting = true
		}
		if r.answered() {
			anyAnswered = true
			continue
		}
		codes[outcomeExit[r.Outcome]] = true
	}
	switch {
	case len(codes) == 0 && anyStarting:
		return exitPartial
	case len(codes) == 0:
		return 0
	case anyAnswered:
		return exitPartial
	case len(codes) == 1:
		for c := range codes {
			return c
		}
	}
	return exitNoneMixed
}

// fanOutKeyColumn is the KEY cell of a view that reads several clusters:
// <cluster>:<workspace>/<name>.
func fanOutKeyColumn() sessionColumn {
	return sessionColumn{name: "key", header: "KEY", pick: func(r sessionRow) string { return r.key }}
}

// clusterSet reads the cluster flags of get. It returns the clusters the
// command names and whether it is a cluster set (a fan-out): --all-clusters,
// or --cluster with two or more names. A single --cluster is not a set and
// keeps today's behavior (R4). Every refusal is a command error, raised
// before any dial.
func clusterSet(resource string, all bool) (names []string, fan bool, err error) {
	isSessions := resource == "sessions" || resource == "session"
	list := strings.Contains(clusterName, ",")
	if (all || list) && !isSessions {
		return nil, false, errors.New("--cluster with a list and --all-clusters apply to get sessions only; name one cluster")
	}
	if all && clusterFlagGiven {
		return nil, false, errors.New("--all-clusters and --cluster cannot be combined")
	}
	if all {
		cfg, err := config.Load()
		if err != nil {
			return nil, false, fmt.Errorf("the client config is unreadable: %w", err)
		}
		for _, c := range cfg.Clusters {
			names = append(names, c.Name)
		}
		if len(names) == 0 {
			return nil, false, errors.New("--all-clusters: the client config names no clusters")
		}
		return names, true, nil
	}
	if !list {
		return nil, false, nil
	}
	seen := map[string]bool{}
	for _, n := range strings.Split(clusterName, ",") {
		n = strings.TrimSpace(n)
		switch {
		case n == "":
			return nil, false, errors.New("--cluster: an empty cluster name in the list")
		case seen[n]:
			return nil, false, fmt.Errorf("--cluster: %q is named twice", n)
		}
		seen[n] = true
		names = append(names, n)
	}
	return names, true, nil
}

// fanOutRefusals are the rules that hold for a view that reads several
// clusters (plan section 2): not inside a seat, and no single-daemon flags.
func fanOutRefusals() error {
	if os.Getenv("MARVEL_SESSION") != "" {
		return errors.New("a view across clusters is refused inside a marvel seat (MARVEL_SESSION is set); run it as the operator")
	}
	if socketPath != "" || identityPath != "" {
		return errors.New("--socket and --identity name one daemon and cannot be used with more than one cluster; each cluster uses its own config entry")
	}
	return nil
}

// runFanOutGet is `get sessions` across a cluster set.
func runFanOutGet(cmd *cobra.Command, names []string, columns string, noTrunc, watching, hasOutput, load bool) error {
	if err := fanOutRefusals(); err != nil {
		return err
	}
	switch {
	case watching:
		return errors.New("--watch across clusters is not available yet")
	case hasOutput:
		return errors.New("-o across clusters is not available yet")
	case load:
		return errors.New("--load applies to get teams only")
	}
	cols, explicit, err := loadSessionColumnsSel(columns)
	if err != nil {
		return err
	}
	records, rows, err := collectFanOut(names, wantsComposer(cols))
	if err != nil {
		return err
	}
	for _, r := range records {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), r.statusLine())
	}
	sort.SliceStable(rows, func(i, j int) bool { return sessionTieLess(rows[i], rows[j]) })
	fit := fitOptions{
		width:    terminalWidth(),
		explicit: explicit,
		noTrunc:  noTrunc,
		lead:     []sessionColumn{fanOutKeyColumn()},
	}
	fmt.Print(renderSessionsView(rows, cols, "", fit))
	if code := fanOutExit(records); code != 0 {
		cmd.SilenceErrors, cmd.SilenceUsage = true, true
		return &exitError{Code: code, Msg: "not every cluster answered"}
	}
	return nil
}

// typedKey returns the session or team key an operator typed as an argument
// of a command that takes one, and whether the command takes one. A key
// holding ':' reads as qualified (plan section 2), and qualified keys wait
// for #829.
func typedKey(cmd *cobra.Command, args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	i, ok := keyArgCommands[cmd.CommandPath()]
	if !ok {
		return "", false
	}
	if i < 0 {
		i = len(args) - 1
	}
	return args[i], true
}

// keyArgCommands maps each command that takes a session or team key to the
// position of that argument; -1 is the last one. A test walks the command
// tree and fails for a command whose Use line takes a key and is missing here.
var keyArgCommands = map[string]int{
	"marvel kill": 0, "marvel inject": 0, "marvel capture": 0, "marvel wait": 0,
	"marvel scale": 0, "marvel shift": 0, "marvel reset-health": 0,
	"marvel plan": 0, "marvel converge": 0, "marvel backend verify": 0,
	"marvel exposure add": 0, "marvel exposure end": 0, "marvel exposure void": 0,
	"marvel view refresh": 0,
	"marvel describe":     -1, "marvel delete": -1,
}
