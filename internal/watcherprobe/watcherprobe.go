// Package watcherprobe is the checker and the loggers of the watcher's claude
// probe kit (docs/design/marvel-watcher.md section 6). It measures claude and
// decides nothing: the kit runs on a scratch claude, its loggers append
// monotonic-stamped lines to one log, and Run turns that log into the c1 to
// c13 result that C2 reads. Nothing here rings, restarts or routes anything.
//
// The keys are c1 to c13, and each check also carries its design name:
//
//	c1  pre_session_hooks            hooks at the trust dialog and the setup menu
//	c2  idle_cost_moves              cost and context movement while idle
//	c3  one_submit_one_stop          one submit and one stop per prompt (the gate for hooks as the turn source)
//	c4  ring_form                    one key: the exact ring form's source and its submit-to-hook latency in ms
//	c5  denial_reason                PermissionDenied.reason and the Notification.notification_type seen
//	c6  interrupt_event              stop, stopfailure or none after Esc
//	c7  nonprompt_moves              turn hooks and statusline movement with no prompt
//	c8  idle_notification_s          seconds from when claude went idle (setup-closed, or the last Stop before the wait window) to the idle_prompt notification, or "never"
//	c9  hook_stdout_reaches_context  whether a hook's stdout reaches the context
//	c10 hook_exit2_effect            what an exit 2 does to a prompt
//	c11 subagent_stamps              subagent hooks and their agent ids
//	c12 stop_background              whether Stop comes before a background task ends
//	c13 version_visible_to_hook      whether a hook can see the claude version
//
// (Section 6 of the design lists twelve measurements; c8 was lost when it was
// condensed, and c4 is the ring form's source and latency together.)
//
// The log is one tab-separated line per event: a monotonic nanosecond stamp, a
// kind, a name and a body. The driver writes mark lines around each scenario
// (the names are in the checks below); the hook and statusline loggers write
// the rest. A scenario whose marks are absent reads not-run, never a guess.
package watcherprobe

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// The kinds of line in the log.
const (
	KindHook    = "hook"
	KindHookEnv = "hookenv"
	KindStatus  = "status"
	KindMark    = "mark"
)

// The statuses a check can take. A measurement that has no right answer is
// observed; a check with a stated condition passes or fails; one whose marks
// are missing is not-run.
const (
	StatusPass     = "pass"
	StatusFail     = "fail"
	StatusObserved = "observed"
	StatusNotRun   = "not-run"
)

// maxHookInput bounds what a logger reads from a hook's stdin.
const maxHookInput = 1 << 20

// Line is one log line.
type Line struct {
	MonoNS int64
	Kind   string
	Name   string
	Body   string
}

var kinds = []string{KindHook, KindHookEnv, KindStatus, KindMark}

var (
	escaper   = strings.NewReplacer(`\`, `\\`, "\t", `\t`, "\n", `\n`, "\r", `\r`)
	unescaper = strings.NewReplacer(`\\`, `\`, `\t`, "\t", `\n`, "\n", `\r`, "\r")
)

// Format renders l as one physical line, so a single write appends it whole.
func Format(l Line) string {
	return fmt.Sprintf("%d\t%s\t%s\t%s\n", l.MonoNS, l.Kind, escaper.Replace(l.Name), escaper.Replace(l.Body))
}

// Parse reads lines written by Format. A malformed line is an error, so a torn
// or hand-edited log cannot yield a result.
func Parse(r io.Reader) ([]Line, error) {
	br := bufio.NewReader(r)
	var out []Line
	for n := 1; ; n++ {
		s, err := br.ReadString('\n')
		if s != "" {
			s = strings.TrimSuffix(s, "\n")
			f := strings.SplitN(s, "\t", 4)
			if len(f) != 4 {
				return nil, fmt.Errorf("watcherprobe: log line %d has %d fields, want 4", n, len(f))
			}
			ns, perr := strconv.ParseInt(f[0], 10, 64)
			if perr != nil {
				return nil, fmt.Errorf("watcherprobe: log line %d: stamp %q: %w", n, f[0], perr)
			}
			if !slices.Contains(kinds, f[1]) {
				return nil, fmt.Errorf("watcherprobe: log line %d: unknown kind %q", n, f[1])
			}
			out = append(out, Line{MonoNS: ns, Kind: f[1], Name: unescaper.Replace(f[2]), Body: unescaper.Replace(f[3])})
		}
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// MonoNS reads the system monotonic clock in nanoseconds. It is one clock for
// every process on the host, so the loggers, the driver and the checker agree.
func MonoNS() int64 {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts); err != nil {
		return 0
	}
	return ts.Nano()
}

// Append adds l to the log at path with one write on an append-only handle,
// so loggers running at once never tear a line.
func Append(path string, l Line) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(Format(l))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

func compact(b []byte) (string, bool) {
	var out bytes.Buffer
	if err := json.Compact(&out, bytes.TrimSpace(b)); err != nil {
		return strings.TrimSpace(string(b)), false
	}
	return out.String(), true
}

// secretFrom reads the credential the loggers must never write: the kit hands
// it to the scratch tmux server as WATCHER_PROBE_KEY, so claude, its hooks and
// its statusline command inherit it.
func secretFrom(environ []string) string {
	for _, e := range environ {
		if v, ok := strings.CutPrefix(e, "WATCHER_PROBE_KEY="); ok {
			return v
		}
	}
	return ""
}

// RecordHook logs one hook firing: the payload on stdin as one hook line, then
// one hookenv line naming the variables claude set for the hook. A variable is
// named when it starts with CLAUDE, and its value is logged only when the name
// ends in VERSION, so a credential never reaches the log. The payload is masked
// first, since a prompt or a notification can quote a key.
func RecordHook(path string, stdin io.Reader, environ []string) error {
	raw, err := io.ReadAll(io.LimitReader(stdin, maxHookInput))
	if err != nil {
		return err
	}
	body, isJSON := compact(raw)
	body = Mask(body, secretFrom(environ))
	name := "unparsed"
	if isJSON {
		var p struct {
			Event string `json:"hook_event_name"`
		}
		if json.Unmarshal([]byte(body), &p) == nil && p.Event != "" {
			name = p.Event
		}
	}
	now := MonoNS()
	if err := Append(path, Line{MonoNS: now, Kind: KindHook, Name: name, Body: body}); err != nil {
		return err
	}
	var vars []string
	for _, e := range environ {
		k, v, _ := strings.Cut(e, "=")
		if !strings.HasPrefix(k, "CLAUDE") {
			continue
		}
		if strings.HasSuffix(k, "VERSION") {
			vars = append(vars, k+"="+v)
		} else {
			vars = append(vars, k)
		}
	}
	slices.Sort(vars)
	return Append(path, Line{MonoNS: MonoNS(), Kind: KindHookEnv, Name: name, Body: strings.Join(vars, " ")})
}

// RecordStatusline logs one statusline refresh, masked like a hook payload, and
// returns the one line claude shows for it.
func RecordStatusline(path string, stdin io.Reader, environ []string) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(stdin, maxHookInput))
	if err != nil {
		return "", err
	}
	body, _ := compact(raw)
	body = Mask(body, secretFrom(environ))
	if err := Append(path, Line{MonoNS: MonoNS(), Kind: KindStatus, Name: "statusline", Body: body}); err != nil {
		return "", err
	}
	return "watcher probe", nil
}

// Check is the answer for one of c1 to c13.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Value  any    `json:"value,omitempty"`
	Note   string `json:"note,omitempty"`
}

// Result is probe_result.json: the build stamp and c1 to c13.
type Result struct {
	BuildStamp string
	Checks     map[string]Check
}

// MarshalJSON flattens the result to {"build_stamp": ..., "c1": ..., ...}.
func (r Result) MarshalJSON() ([]byte, error) {
	m := make(map[string]any, len(r.Checks)+1)
	for k, v := range r.Checks {
		m[k] = v
	}
	m["build_stamp"] = r.BuildStamp
	return json.Marshal(m)
}

// window is the stretch between a start mark and the next end mark.
type window struct {
	from, to int64
	ok       bool
}

func markAt(lines []Line, name string, after int64) (int64, bool) {
	for _, l := range lines {
		if l.Kind == KindMark && l.Name == name && l.MonoNS >= after {
			return l.MonoNS, true
		}
	}
	return 0, false
}

func windowOf(lines []Line, start, end string) window {
	from, ok := markAt(lines, start, 0)
	if !ok {
		return window{}
	}
	to, ok := markAt(lines, end, from)
	if !ok {
		return window{}
	}
	return window{from: from, to: to, ok: true}
}

func (w window) has(l Line) bool { return l.MonoNS >= w.from && l.MonoNS <= w.to }

func inWindow(lines []Line, w window, kind string) []Line {
	var out []Line
	for _, l := range lines {
		if l.Kind == kind && w.has(l) {
			out = append(out, l)
		}
	}
	return out
}

func isStop(l Line) bool { return l.Name == "Stop" || l.Name == "StopFailure" }

func countNamed(ls []Line, names ...string) int {
	n := 0
	for _, l := range ls {
		if slices.Contains(names, l.Name) {
			n++
		}
	}
	return n
}

func notRun(why string) Check { return Check{Status: StatusNotRun, Note: why} }

func jsonObject(body string) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(body), &m) != nil {
		return nil
	}
	return m
}

func jsonString(body, key string) string {
	var s string
	if raw, ok := jsonObject(body)[key]; ok {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

// movement reads what a statusline sample says about cost and context.
func movement(body string) (cost, ctx string) {
	m := jsonObject(body)
	if c := jsonObject(string(m["cost"])); c != nil {
		cost = string(c["total_cost_usd"])
	}
	return cost, string(m["context_window"])
}

func statusChanges(samples []Line) (cost, ctx bool, changes int) {
	for i := 1; i < len(samples); i++ {
		c0, x0 := movement(samples[i-1].Body)
		c1, x1 := movement(samples[i].Body)
		cost = cost || c0 != c1
		ctx = ctx || x0 != x1
		if c0 != c1 || x0 != x1 {
			changes++
		}
	}
	return cost, ctx, changes
}

// names are the design names of c1 to c13.
var names = map[string]string{
	"c1": "pre_session_hooks", "c2": "idle_cost_moves", "c3": "one_submit_one_stop", "c4": "ring_form",
	"c5": "denial_reason", "c6": "interrupt_event", "c7": "nonprompt_moves", "c8": "idle_notification_s",
	"c9": "hook_stdout_reaches_context", "c10": "hook_exit2_effect", "c11": "subagent_stamps",
	"c12": "stop_background", "c13": "version_visible_to_hook",
}

// Run evaluates the log into the c1 to c13 result.
func Run(lines []Line, stamp string) Result {
	r := Result{BuildStamp: stamp, Checks: map[string]Check{
		"c1": c1(lines), "c2": c2(lines), "c3": c3(lines), "c4": c4(lines), "c5": c5(lines),
		"c6": c6(lines), "c7": c7(lines), "c8": c8(lines), "c9": c9(lines), "c10": c10(lines),
		"c11": c11(lines), "c12": c12(lines), "c13": c13(lines),
	}}
	for k, c := range r.Checks {
		c.Name = names[k]
		r.Checks[k] = c
	}
	return r
}

// c1: hook firings from trust-open to setup-closed, or to trust-closed when the
// setup menu was not marked.
func c1(lines []Line) Check {
	w := windowOf(lines, "trust-open", "trust-closed")
	if !w.ok {
		return notRun("needs trust-open and trust-closed marks")
	}
	if end, ok := markAt(lines, "setup-closed", w.to); ok {
		w.to = end
	}
	return Check{Status: StatusObserved, Value: len(inWindow(lines, w, KindHook))}
}

// c2: statusline cost and context between idle-start and idle-end.
func c2(lines []Line) Check {
	w := windowOf(lines, "idle-start", "idle-end")
	if !w.ok {
		return notRun("needs idle-start and idle-end marks")
	}
	samples := inWindow(lines, w, KindStatus)
	cost, ctx, _ := statusChanges(samples)
	return Check{Status: StatusObserved, Value: map[string]any{"cost_moved": cost, "context_moved": ctx, "samples": len(samples)}}
}

// c3: exactly one UserPromptSubmit and one Stop or StopFailure inside each
// prompt-sent to prompt-done window. Hooks may be the turn source only if this
// passes.
func c3(lines []Line) Check {
	var per []map[string]any
	pass, sawPrompt := true, false
	for _, l := range lines {
		if l.Kind != KindMark || l.Name != "prompt-sent" {
			continue
		}
		sawPrompt = true
		entry := map[string]any{"prompt": l.Body}
		done, ok := int64(0), false
		for _, d := range lines {
			if d.Kind == KindMark && d.Name == "prompt-done" && d.Body == l.Body && d.MonoNS >= l.MonoNS {
				done, ok = d.MonoNS, true
				break
			}
		}
		if !ok {
			entry["finished"] = false
			per = append(per, entry)
			pass = false
			continue
		}
		w := window{from: l.MonoNS, to: done, ok: true}
		hooks := inWindow(lines, w, KindHook)
		submits, stops := countNamed(hooks, "UserPromptSubmit"), 0
		for _, h := range hooks {
			if isStop(h) {
				stops++
			}
		}
		entry["submits"], entry["stops"] = submits, stops
		per = append(per, entry)
		if submits != 1 || stops != 1 {
			pass = false
		}
	}
	if !sawPrompt {
		return notRun("needs prompt-sent and prompt-done marks")
	}
	st := StatusPass
	if !pass {
		st = StatusFail
	}
	return Check{Status: st, Value: per}
}

// c4: the first UserPromptSubmit after ring-sent, with its source and the
// latency from the ring, for the exact ring form the driver sent.
func c4(lines []Line) Check {
	sent, ok := markAt(lines, "ring-sent", 0)
	if !ok {
		return notRun("needs a ring-sent mark")
	}
	ringText, _ := markBody(lines, "ring-sent")
	end := int64(1<<63 - 1)
	if done, ok := markAt(lines, "ring-done", sent); ok {
		end = done
	}
	for _, l := range lines {
		if l.Kind == KindHook && l.Name == "UserPromptSubmit" && l.MonoNS >= sent && l.MonoNS <= end {
			var keys []string
			for k := range jsonObject(l.Body) {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			return Check{Status: StatusObserved, Value: map[string]any{
				"event": l.Name, "source": jsonString(l.Body, "source"), "payload_keys": keys,
				"prompt_matches_ring": jsonString(l.Body, "prompt") == ringText,
				"latency_ms":          (l.MonoNS - sent) / 1_000_000,
			}}
		}
	}
	return Check{Status: StatusFail, Note: "the ring produced no UserPromptSubmit before ring-done"}
}

// c5: the reason on PermissionDenied and the notification types seen, between the
// denial marks.
func c5(lines []Line) Check {
	w := windowOf(lines, "denial-start", "denial-end")
	if !w.ok {
		return notRun("needs denial-start and denial-end marks")
	}
	reason, types := "", []string{}
	for _, h := range inWindow(lines, w, KindHook) {
		switch h.Name {
		case "PermissionDenied":
			if reason == "" {
				reason = jsonString(h.Body, "reason")
			}
		case "Notification":
			if t := jsonString(h.Body, "notification_type"); t != "" && !slices.Contains(types, t) {
				types = append(types, t)
			}
		}
	}
	return Check{Status: StatusObserved, Value: map[string]any{"reason": reason, "notification_types": types}}
}

// c6: the first Stop or StopFailure after the interrupt, or none.
func c6(lines []Line) Check {
	w := windowOf(lines, "interrupt-sent", "interrupt-end")
	if !w.ok {
		return notRun("needs interrupt-sent and interrupt-end marks")
	}
	for _, h := range inWindow(lines, w, KindHook) {
		switch h.Name {
		case "Stop":
			return Check{Status: StatusObserved, Value: "stop"}
		case "StopFailure":
			return Check{Status: StatusObserved, Value: "stopfailure"}
		}
	}
	return Check{Status: StatusObserved, Value: "none"}
}

// c7: with no prompt sent, a turn hook is a failure; statusline movement is
// recorded for the report and is not one.
func c7(lines []Line) Check {
	w := windowOf(lines, "quiet-start", "quiet-end")
	if !w.ok {
		return notRun("needs quiet-start and quiet-end marks")
	}
	turn := countNamed(inWindow(lines, w, KindHook), "UserPromptSubmit", "Stop", "StopFailure")
	_, _, changes := statusChanges(inWindow(lines, w, KindStatus))
	st := StatusPass
	if turn != 0 {
		st = StatusFail
	}
	return Check{Status: st, Value: map[string]any{"turn_hooks": turn, "statusline_changes": changes}}
}

// c8: seconds from when claude went idle to the first idle_prompt notification
// up to idle-wait-end, or "never". Claude went idle at the later of setup-closed
// and the last Stop or StopFailure at or before the wait window opened; with
// neither, at the window's own start. The note says which, and a notification
// before the window opened still counts, since claude was already idle.
func c8(lines []Line) Check {
	w := windowOf(lines, "idle-wait-start", "idle-wait-end")
	if !w.ok {
		return notRun("needs idle-wait-start and idle-wait-end marks")
	}
	from, basis := w.from, "idle-wait-start"
	if t, ok := markAt(lines, "setup-closed", 0); ok && t <= w.from {
		from, basis = t, "setup-closed"
	}
	for _, l := range lines {
		if l.Kind == KindHook && isStop(l) && l.MonoNS <= w.from && l.MonoNS >= from {
			from, basis = l.MonoNS, "the last Stop or StopFailure"
		}
	}
	note := "measured from " + basis
	for _, h := range lines {
		if h.Kind == KindHook && h.Name == "Notification" && h.MonoNS >= from && h.MonoNS <= w.to &&
			jsonString(h.Body, "notification_type") == "idle_prompt" {
			return Check{Status: StatusObserved, Value: float64((h.MonoNS-from)/100_000_000) / 10, Note: note}
		}
	}
	return Check{Status: StatusObserved, Value: "never", Note: note}
}

// c9: the driver's reading of whether a hook's stdout reached the context.
func c9(lines []Line) Check {
	for _, l := range lines {
		if l.Kind == KindMark && l.Name == "stdout-canary" {
			switch l.Body {
			case "yes":
				return Check{Status: StatusObserved, Value: true}
			case "no":
				return Check{Status: StatusObserved, Value: false}
			}
			return Check{Status: StatusFail, Note: "the stdout-canary mark held neither yes nor no"}
		}
	}
	return notRun("needs a stdout-canary mark")
}

// c10: the driver's reading of what an exit 2 did to a prompt.
func c10(lines []Line) Check {
	e2, ok := markBody(lines, "exit2-effect")
	if !ok {
		return notRun("needs an exit2-effect mark")
	}
	return Check{Status: StatusObserved, Value: e2}
}

func markBody(lines []Line, name string) (string, bool) {
	for _, l := range lines {
		if l.Kind == KindMark && l.Name == name {
			return l.Body, true
		}
	}
	return "", false
}

// c11: subagent hooks, and whether they carry an agent id, between the marks.
func c11(lines []Line) Check {
	w := windowOf(lines, "subagent-start", "subagent-end")
	if !w.ok {
		return notRun("needs subagent-start and subagent-end marks")
	}
	events, withID, stops := 0, 0, 0
	for _, h := range inWindow(lines, w, KindHook) {
		switch {
		case h.Name == "SubagentStart" || h.Name == "SubagentStop":
			events++
			if jsonString(h.Body, "agent_id") != "" {
				withID++
			}
		case isStop(h):
			stops++
		}
	}
	return Check{Status: StatusObserved, Value: map[string]any{"subagent_events": events, "with_agent_id": withID, "turn_stops": stops}}
}

// c12: whether the turn's Stop came before the background task finished.
func c12(lines []Line) Check {
	w := windowOf(lines, "bg-start", "bg-end")
	if !w.ok {
		return notRun("needs bg-start and bg-end marks")
	}
	done, ok := markAt(lines, "bg-task-done", w.from)
	if !ok || done > w.to {
		return notRun("needs a bg-task-done mark inside the window")
	}
	before := false
	for _, h := range inWindow(lines, w, KindHook) {
		if isStop(h) && h.MonoNS < done {
			before = true
		}
	}
	return Check{Status: StatusObserved, Value: map[string]any{"stop_before_task_done": before}}
}

// c13: whether a hook's payload or environment carries the claude version.
func c13(lines []Line) Check {
	seen, payload := false, false
	env := ""
	for _, l := range lines {
		switch l.Kind {
		case KindHook:
			seen = true
			if _, ok := jsonObject(l.Body)["version"]; ok {
				payload = true
			}
		case KindHookEnv:
			seen = true
			for _, tok := range strings.Fields(l.Body) {
				if env == "" && strings.Contains(tok, "VERSION=") {
					env = tok
				}
			}
		}
	}
	if !seen {
		return notRun("no hook ran")
	}
	return Check{Status: StatusObserved, Value: map[string]any{"payload_field": payload, "env": env}}
}

// minFragment is the shortest piece of the secret that Mask hides. Claude's
// custom-key dialog is reported to show part of a key, so a piece counts as
// well as the whole.
const minFragment = 8

var keyShape = regexp.MustCompile(`\bsk-[A-Za-z0-9_.-]{3,}`)

// Mask hides key text in captured pane text: anything shaped like an API key,
// and every run of minFragment or more characters that also occurs in secret.
// It keeps the line structure, so a masked capture still reads as the pane.
func Mask(text, secret string) string {
	text = keyShape.ReplaceAllString(text, "[masked]")
	if len(secret) < minFragment {
		return text
	}
	var out strings.Builder
	for i := 0; i < len(text); {
		n := 0
		for i+n < len(text) && strings.Contains(secret, text[i:i+n+1]) {
			n++
		}
		if n >= minFragment {
			out.WriteString("[masked]")
			i += n
			continue
		}
		out.WriteByte(text[i])
		i++
	}
	return out.String()
}
