package main

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
)

// The totals line is `label   N sessions   cpu ...   rss ...   tout ...
// rate ...`, fields apart by two or more spaces and each field's text kept
// together (design section 2). These helpers read a field by its name; they
// assert no figure form beyond what each test names.

var fieldGap = regexp.MustCompile(`\s{2,}`)

// totalsFieldText is the text of one named field, or "" when the line has no
// such field.
func totalsFieldText(line, name string) string {
	for _, f := range fieldGap.Split(strings.TrimSpace(line), -1) {
		if rest, ok := strings.CutPrefix(f, name+" "); ok {
			return rest
		}
		if f == name {
			return ""
		}
	}
	return ""
}

func hasTotalsField(line, name string) bool {
	for _, f := range fieldGap.Split(strings.TrimSpace(line), -1) {
		if f == name || strings.HasPrefix(f, name+" ") {
			return true
		}
	}
	return false
}

var parenCount = regexp.MustCompile(`\((\d+)\)`)

// totalsCounts is every "(n)" in a field's text, in order.
func totalsCounts(text string) []string {
	var out []string
	for _, m := range parenCount.FindAllStringSubmatch(text, -1) {
		out = append(out, m[1])
	}
	return out
}

func sessionsCount(line string) string {
	m := regexp.MustCompile(`(\d+) sessions`).FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	return m[1]
}

// withTout is a token-metered row: cumulative output tokens n.
func withTout(s api.Session, n int) api.Session {
	s.SpendOut = &n
	return s
}

// withMetrics is a row the sampler has read.
func withMetrics(s api.Session, cpu float64, rss int64) api.Session {
	s.MetricsAt = rateNow
	s.CPUPercent = cpu
	s.RSSBytes = rss
	return s
}

func inState(s api.Session, st api.SessionState) api.Session {
	s.State = st
	return s
}

func renderTotals(t *testing.T, rows []api.Session) string {
	t.Helper()
	withRateClock(t)
	return renderTotalsLine(rows, rateClock(), "totals", 0)
}

// A row whose CPU cell is "-" (never sampled) or whose process is gone adds
// nothing to cpu or rss and is not counted.
func TestTotalsCPUAndRSSSkipUnsampledAndGoneRows(t *testing.T) {
	const gib = int64(1) << 30
	base := api.Session{Name: "a", Workspace: "ws", Team: "squad", Role: "worker"}
	rows := []api.Session{
		withMetrics(inState(base, api.SessionRunning), 10, gib),
		inState(base, api.SessionRunning), // MetricsAt zero
		withMetrics(inState(base, api.SessionCrashed), 50, 2*gib),
		withMetrics(inState(base, api.SessionFailed), 60, 3*gib),
		withMetrics(inState(base, api.SessionSucceeded), 70, 4*gib),
	}
	line := renderTotals(t, rows)
	for _, f := range []string{"cpu", "rss"} {
		if got := totalsCounts(totalsFieldText(line, f)); len(got) != 1 || got[0] != "1" {
			t.Errorf("%s counts = %v, want [1] (line %q)", f, got, line)
		}
	}
	if !strings.Contains(totalsFieldText(line, "rss"), formatBytes(gib)) {
		t.Errorf("rss = %q, want the single 1 GiB value %q", totalsFieldText(line, "rss"), formatBytes(gib))
	}
}

// RATE sums fresh readings only: a stale ("?") and a never-sampled ("-") cell
// add nothing and are not counted.
func TestTotalsRateSkipsExpiredAndNeverMeasured(t *testing.T) {
	fresh := rateSession("fresh", 40, 30*time.Second, 30*time.Second) // 14.1/s
	stale := rateSession("stale", 40, 11*time.Minute, time.Minute)    // ?
	never := api.Session{Name: "never", Workspace: "ws", Team: "squad", Role: "worker", State: api.SessionRunning}
	line := renderTotals(t, []api.Session{fresh, stale, never})
	text := totalsFieldText(line, "rate")
	if got := totalsCounts(text); len(got) != 1 || got[0] != "1" {
		t.Errorf("rate counts = %v, want [1] (line %q)", got, line)
	}
	if !strings.Contains(text, "14.1") {
		t.Errorf("rate = %q, want the fresh row's 14.1 and nothing from the stale one", text)
	}
}

// A seat that went quiet shows 0/s, which is a value: it counts and adds 0.
func TestTotalsQuietSeatCountsAsZeroValue(t *testing.T) {
	quiet := rateSession("quiet", 40, 11*time.Minute, 11*time.Minute) // exactly 0/s
	line := renderTotals(t, []api.Session{quiet})
	text := totalsFieldText(line, "rate")
	if got := totalsCounts(text); len(got) != 1 || got[0] != "1" {
		t.Errorf("rate counts = %v, want [1] (line %q)", got, line)
	}
	if strings.HasPrefix(text, "-") || !strings.Contains(text, "0") {
		t.Errorf("rate = %q, want a zero value, not a dash", text)
	}
}

// With nothing measured every field prints a dash, never a zero and never
// "(0)".
func TestTotalsFieldWithNoValuePrintsDash(t *testing.T) {
	bare := api.Session{Name: "bare", Workspace: "ws", Team: "squad", Role: "worker", State: api.SessionRunning}
	line := renderTotals(t, []api.Session{bare})
	if line == "" {
		t.Fatal("no totals line")
	}
	for _, f := range []string{"cpu", "rss", "tout", "rate"} {
		if !hasTotalsField(line, f) {
			t.Errorf("no %s field in %q", f, line)
			continue
		}
		if text := totalsFieldText(line, f); !strings.HasPrefix(text, "-") {
			t.Errorf("%s = %q, want a dash", f, text)
		}
	}
	if strings.Contains(line, "(0)") {
		t.Errorf("a field with no value printed a zero count: %q", line)
	}
}

// The sessions figure counts every row the table prints, whatever its state.
func TestTotalsSessionsCountsEveryRow(t *testing.T) {
	base := api.Session{Workspace: "ws", Team: "squad", Role: "worker"}
	var rows []api.Session
	for i, st := range []api.SessionState{api.SessionRunning, api.SessionCrashed, api.SessionSucceeded, api.SessionFailed} {
		r := inState(base, st)
		r.Name = string(rune('a' + i))
		rows = append(rows, r)
	}
	line := renderTotals(t, rows)
	if got := sessionsCount(line); got != "4" {
		t.Errorf("sessions = %q, want 4 (line %q)", got, line)
	}
}

// Over a token-metered fixture set the count in each field equals the
// number of rows whose rendered cell is a value, read from newSessionRow, so
// the line never re-derives which rows have one. The statusline dollar
// fields are held until Open question 2 is ruled.
func TestTotalsAgreeWithRenderedCells(t *testing.T) {
	const gib = int64(1) << 30
	mk := func(name string) api.Session {
		return api.Session{Name: name, Workspace: "ws", Team: "squad", Role: "worker", State: api.SessionRunning}
	}
	rows := []api.Session{
		withTout(withMetrics(mk("a"), 10, gib), 1000),
		withTout(mk("b"), 2000),
		withMetrics(inState(mk("c"), api.SessionCrashed), 5, gib),
		mk("d"),
		withTout(withMetrics(mk("e"), 7, 2*gib), 500),
	}
	rows[0].OutRate = rateSession("a", 40, 30*time.Second, 30*time.Second).OutRate
	rows[0].ContextAt = rateNow.Add(-30 * time.Second)
	rows[1].OutRate = rateSession("b", 40, 11*time.Minute, time.Minute).OutRate // stale
	rows[1].ContextAt = rateNow.Add(-time.Minute)
	rows[4].OutRate = rateSession("e", 40, 11*time.Minute, 11*time.Minute).OutRate // quiet, 0/s
	rows[4].ContextAt = rateNow.Add(-11 * time.Minute)

	withRateClock(t)
	var cpu, rss, tout, rate int
	for _, r := range rows {
		cells := newSessionRow(r)
		if cells.cpu != "-" {
			cpu++
		}
		if cells.rss != "-" {
			rss++
		}
		if cells.tout != "-" {
			tout++
		}
		if cells.rate != "-" && cells.rate != "?" {
			rate++
		}
	}
	line := renderTotalsLine(rows, rateClock(), "totals", 0)
	want := map[string]int{"cpu": cpu, "rss": rss, "tout": tout, "rate": rate}
	for f, n := range want {
		got := totalsCounts(totalsFieldText(line, f))
		if len(got) != 1 || got[0] != strconv.Itoa(n) {
			t.Errorf("%s counts = %v, want [%d] from the rendered cells (line %q)", f, got, n, line)
		}
	}
}

// Wider than the terminal, the line breaks between fields onto a further line
// with the same label; no field is split. At width 0 it is one line.
func TestTotalsWrapBetweenFields(t *testing.T) {
	const gib = int64(1) << 30
	rows := []api.Session{
		withTout(withMetrics(rateSession("a", 40, 30*time.Second, 30*time.Second), 10, gib), 1000),
		withTout(withMetrics(rateSession("b", 40, 30*time.Second, 30*time.Second), 20, 2*gib), 2000),
	}
	withRateClock(t)
	one := renderTotalsLine(rows, rateClock(), "totals", 0)
	if strings.Count(strings.TrimRight(one, "\n"), "\n") != 0 || one == "" {
		t.Fatalf("width 0 = %q, want one line", one)
	}
	wide := fieldsOf(one, "totals")
	const width = 40
	wrapped := renderTotalsLine(rows, rateClock(), "totals", width)
	lines := strings.Split(strings.TrimRight(wrapped, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("width %d gave %d line(s), want 2 or more:\n%s", width, len(lines), wrapped)
	}
	var got []string
	for _, l := range lines {
		if !strings.HasPrefix(l, "totals") {
			t.Errorf("line %q does not start with the label", l)
		}
		if len(l) > width {
			t.Errorf("line %q is %d wide, over %d", l, len(l), width)
		}
		got = append(got, fieldsOf(l, "totals")...)
	}
	sort.Strings(got)
	sort.Strings(wide)
	if strings.Join(got, "|") != strings.Join(wide, "|") {
		t.Errorf("wrapped fields = %q, want the unwrapped line's %q", got, wide)
	}
}

func fieldsOf(line, label string) []string {
	rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), label))
	if rest == "" {
		return nil
	}
	return fieldGap.Split(rest, -1)
}

var totalsLineRE = regexp.MustCompile(`(?m)^totals\b.*$`)

// get sessions with --header prints the totals line into a pipe, and on a
// terminal by default.
func TestGetSessionsHeaderFlagPrintsTotalsIntoPipe(t *testing.T) {
	out := getSessionsOutput(t, false, "--header")
	if !totalsLineRE.MatchString(out) {
		t.Errorf("--header into a pipe has no totals line:\n%s", out)
	}
}

func TestGetSessionsTTYPrintsTotals(t *testing.T) {
	out := getSessionsOutput(t, true)
	if !totalsLineRE.MatchString(out) {
		t.Errorf("a terminal read has no totals line:\n%s", out)
	}
}

func watchFrameState(rows []api.Session, cols string, t *testing.T) *watchSort {
	return &watchSort{
		columns:      columnsOrFatal(t, cols, nil),
		lastSessions: rows,
		showHeader:   true,
		fetched:      true,
	}
}

// The watch frame draws totals from the clock the cells use, rateClock, not
// from the frame's own now: a row fresh at rateClock() shows 14.1/s and the
// line sums that same 14.1, though a frame an hour later would read it quiet.
func TestTotalsUseTheCellClock(t *testing.T) {
	withRateClock(t)
	rows := []api.Session{rateSession("a", 40, 30*time.Second, 30*time.Second)} // 14.1/s at rateNow
	frame := renderWatchFrame(watchFrameState(rows, "name,rate", t), time.Second, rateNow.Add(time.Hour))
	if !strings.Contains(frame, "14.1/s") {
		t.Fatalf("setup: the RATE cell does not show the fresh value:\n%s", frame)
	}
	line := totalsLineRE.FindString(frame)
	if line == "" {
		t.Fatalf("no totals line in the frame:\n%s", frame)
	}
	text := totalsFieldText(line, "rate")
	if !strings.Contains(text, "14.1") {
		t.Errorf("rate = %q, want the cell's 14.1 (line %q)", text, line)
	}
}

func TestWatchFrameCarriesTotals(t *testing.T) {
	withRateClock(t)
	rows := []api.Session{rateSession("a", 40, 30*time.Second, 30*time.Second), rateSession("b", 40, 30*time.Second, 30*time.Second)}
	frame := renderWatchFrame(watchFrameState(rows, "name", t), time.Second, rateNow)
	line := totalsLineRE.FindString(frame)
	if line == "" {
		t.Fatalf("no totals line in the watch frame:\n%s", frame)
	}
	if got := sessionsCount(line); got != "2" {
		t.Errorf("sessions = %q, want 2 (line %q)", got, line)
	}
}

func fakeRows(ws string, names ...string) []api.Session {
	var rows []api.Session
	for _, n := range names {
		rows = append(rows, row(ws, n))
	}
	return rows
}

// A fan-out read prints one totals line per answered cluster, each from that
// cluster's own rows (a has 2, b has 3), in the status-line order.
func TestFanOutTotalsOneLinePerAnsweredCluster(t *testing.T) {
	a := startFake(t, fakeAnswers, fakeRows("wa", "a1", "a2"))
	b := startFake(t, fakeAnswers, fakeRows("wb", "b1", "b2", "b3"))
	clusterConfigShowing(t, config.Display{TotalsFleet: off()}, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})
	_, stdout, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b", "--header")
	lines := totalsLineRE.FindAllString(stdout, -1)
	if len(lines) != 2 {
		t.Fatalf("got %d totals lines, want 2:\n%s", len(lines), stdout)
	}
	for i, want := range []struct{ label, n string }{{"totals a", "2"}, {"totals b", "3"}} {
		if !strings.HasPrefix(lines[i], want.label+" ") {
			t.Errorf("line %d = %q, want it to start %q", i, lines[i], want.label)
		}
		if got := sessionsCount(lines[i]); got != want.n {
			t.Errorf("line %d sessions = %q, want %s (its own rows only): %q", i, got, want.n, lines[i])
		}
	}
}

// A cluster that did not answer prints no totals line.
func TestFanOutTotalsSkipUnansweredCluster(t *testing.T) {
	fastTimer(t, 500*time.Millisecond)
	a := startFake(t, fakeAnswers, fakeRows("wa", "a1"))
	h := startFake(t, fakeHung, nil)
	clusterConfigShowing(t, config.Display{TotalsFleet: off()}, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "h", Socket: h.sock})
	_, stdout, _ := runFanGet(t, "get", "sessions", "--cluster", "a,h", "--header")
	lines := totalsLineRE.FindAllString(stdout, -1)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "totals a ") {
		t.Errorf("totals lines = %q, want exactly one, for a:\n%s", lines, stdout)
	}
}

// Guards, green at 06e94526 and kept green: the pipe table and the -o json
// documents carry no totals and do not change.
const pipeGolden = "WORKSPACE  TEAM   ROLE    GEN  AGENT NAME  STATE    HEALTH   CTX%  ACTIVE%  LAST-ACTIVE  AGE  LLM\n" +
	"ws         squad  worker  0    agent-0     running  unknown  -     -        -            -    -\n"

func TestGetSessionsPipeUnchanged(t *testing.T) {
	if got := getSessionsOutput(t, false); got != pipeGolden {
		t.Errorf("piped get sessions changed:\n%q\nwant\n%q", got, pipeGolden)
	}
}

func sha(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }

const (
	jsonGoldenSHA    = "321573627246d16e4ff85a18a3f5d6cd3a4d90c4faf4186dc0069db1aac83b2c"
	fanJSONGoldenSHA = "672526d38a70fc139d3463e667eb97a24967d2524cbe1f8f2ef7a3f7c1de95fa"
)

var volatile = regexp.MustCompile(`"(as_of|socket|address)": "[^"]*"`)

func TestGetSessionsJSONUnchanged(t *testing.T) {
	if got := sha(getSessionsOutput(t, false, "-o", "json")); got != jsonGoldenSHA {
		t.Errorf("single-cluster -o json sha256 = %s, want %s", got, jsonGoldenSHA)
	}
	a := startFake(t, fakeAnswers, fakeRows("wa", "a1", "a2"))
	b := startFake(t, fakeAnswers, fakeRows("wb", "b1"))
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})
	_, stdout, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b", "-o", "json", "--header")
	norm := volatile.ReplaceAllString(stdout, `"$1": "X"`)
	if got := sha(norm); got != fanJSONGoldenSHA {
		t.Errorf("fan-out -o json (volatile fields masked) sha256 = %s, want %s\n%s", got, fanJSONGoldenSHA, norm)
	}
}
