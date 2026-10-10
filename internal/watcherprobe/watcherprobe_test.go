package watcherprobe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const ms = int64(1_000_000)

func hook(ns int64, event string) Line {
	return Line{MonoNS: ns, Kind: KindHook, Name: event, Body: fmt.Sprintf(`{"hook_event_name":%q}`, event)}
}

func hookBody(ns int64, event, body string) Line {
	return Line{MonoNS: ns, Kind: KindHook, Name: event, Body: body}
}

func mark(ns int64, name, body string) Line {
	return Line{MonoNS: ns, Kind: KindMark, Name: name, Body: body}
}

func status(ns int64, cost string, tokens int) Line {
	return Line{MonoNS: ns, Kind: KindStatus, Name: "statusline", Body: fmt.Sprintf(`{"cost":{"total_cost_usd":%s},"context_window":{"total_input_tokens":%d}}`, cost, tokens)}
}

func get(t *testing.T, r Result, key string) Check {
	t.Helper()
	c, ok := r.Checks[key]
	if !ok {
		t.Fatalf("result has no %s", key)
	}
	return c
}

func TestFormatAndParseRoundTripAwkwardBodies(t *testing.T) {
	in := []Line{
		{MonoNS: 5, Kind: KindMark, Name: "a", Body: ""},
		{MonoNS: 6, Kind: KindHook, Name: "Stop", Body: "tab\there\nnewline\\backslash \\n literal"},
		{MonoNS: 7, Kind: KindStatus, Name: "statusline", Body: strings.Repeat("x", 9000)},
	}
	var b bytes.Buffer
	for _, l := range in {
		b.WriteString(Format(l))
	}
	if strings.Count(b.String(), "\n") != len(in) {
		t.Fatalf("each line must be one physical line; got %d newlines for %d lines", strings.Count(b.String(), "\n"), len(in))
	}
	out, err := Parse(&b)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Fatalf("parsed %d lines, want %d", len(out), len(in))
	}
	for i := range in {
		if out[i] != in[i] {
			t.Errorf("line %d: got %+v, want %+v", i, out[i], in[i])
		}
	}
}

func TestParseRefusesAMalformedLine(t *testing.T) {
	for name, in := range map[string]string{
		"too few fields":  "12\thook\n",
		"a bad stamp":     "soon\thook\tStop\t{}\n",
		"an unknown kind": "12\tlog\tStop\t{}\n",
	} {
		if _, err := Parse(strings.NewReader(in)); err == nil {
			t.Errorf("%s: Parse accepted %q", name, in)
		}
	}
}

func TestMonoNSNeverGoesBackwards(t *testing.T) {
	a := MonoNS()
	b := MonoNS()
	if a <= 0 || b < a {
		t.Errorf("MonoNS gave %d then %d", a, b)
	}
}

// Many loggers run at once, one per hook firing, so a line must never be torn.
func TestAppendKeepsEveryLineWholeUnderConcurrency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.tsv")
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				if err := Append(path, Line{MonoNS: MonoNS(), Kind: KindHook, Name: "Stop", Body: strings.Repeat("y", 8000)}); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	lines, err := Parse(f)
	if err != nil {
		t.Fatalf("a torn line: %v", err)
	}
	if len(lines) != 200 {
		t.Errorf("%d lines, want 200", len(lines))
	}
}

func TestRecordHookNamesTheEventAndKeepsNoSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.tsv")
	env := []string{"HOME=/x", "CLAUDE_CODE_OAUTH_TOKEN=sekret-value", "ANTHROPIC_API_KEY=sekret-key", "CLAUDE_CODE_VERSION=2.1.296", "CLAUDECODE=1"}
	err := RecordHook(path, strings.NewReader("{\n \"hook_event_name\": \"UserPromptSubmit\",\n \"prompt\": \"hi\"\n}\n"), env)
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordHook(path, strings.NewReader("not json"), env); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, []byte("sekret")) {
		t.Fatalf("a credential reached the log:\n%s", raw)
	}
	lines, err := Parse(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	var envLine string
	for _, l := range lines {
		names = append(names, l.Kind+":"+l.Name)
		if l.Kind == KindHookEnv {
			envLine = l.Body
		}
	}
	if names[0] != "hook:UserPromptSubmit" || names[1] != "hookenv:UserPromptSubmit" || names[2] != "hook:unparsed" {
		t.Errorf("names = %v", names)
	}
	for _, want := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_VERSION=2.1.296", "CLAUDECODE"} {
		if !strings.Contains(envLine, want) {
			t.Errorf("env line %q lacks %q", envLine, want)
		}
	}
	if strings.Contains(envLine, "ANTHROPIC") || strings.Contains(envLine, "HOME") {
		t.Errorf("env line %q names a variable that is not claude's", envLine)
	}
}

func TestRecordStatuslineAppendsAndPrintsOneLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.tsv")
	out, err := RecordStatusline(path, strings.NewReader(`{"cost":{"total_cost_usd":0.5}}`), nil)
	if err != nil || out == "" || strings.Contains(out, "\n") {
		t.Fatalf("printed %q, %v; want one line", out, err)
	}
	raw, _ := os.ReadFile(path)
	lines, err := Parse(bytes.NewReader(raw))
	if err != nil || len(lines) != 1 || lines[0].Kind != KindStatus || !strings.Contains(lines[0].Body, "0.5") {
		t.Errorf("log = %+v, %v", lines, err)
	}
}

// The keys stay c1 to c13; each check also carries the name it has in the
// design, so a reader of probe_result.json need not know the numbering.
var checkNames = map[string]string{
	"c1": "pre_session_hooks", "c2": "idle_cost_moves", "c3": "one_submit_one_stop", "c4": "ring_form",
	"c5": "denial_reason", "c6": "interrupt_event", "c7": "nonprompt_moves", "c8": "idle_notification_s",
	"c9": "hook_stdout_reaches_context", "c10": "hook_exit2_effect", "c11": "subagent_stamps",
	"c12": "stop_background", "c13": "version_visible_to_hook",
}

func TestCheckWithNothingLoggedIsNotRun(t *testing.T) {
	r := Run(nil, "claude 2.1.296")
	if len(r.Checks) != 13 {
		t.Fatalf("%d checks, want 13", len(r.Checks))
	}
	for i := 1; i <= 13; i++ {
		key := fmt.Sprintf("c%d", i)
		c := get(t, r, key)
		if c.Status != StatusNotRun {
			t.Errorf("%s = %q with an empty log, want %q", key, c.Status, StatusNotRun)
		}
		if c.Name != checkNames[key] {
			t.Errorf("%s is named %q, want %q", key, c.Name, checkNames[key])
		}
	}
}

func TestResultJSONHasExactlyTheBuildStampAndC1ToC13(t *testing.T) {
	b, err := json.Marshal(Run(nil, "claude 2.1.296"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != 14 {
		t.Errorf("%d keys, want 14: %s", len(m), b)
	}
	for _, k := range []string{"build_stamp", "c1", "c2", "c3", "c4", "c5", "c6", "c7", "c8", "c9", "c10", "c11", "c12", "c13"} {
		if _, ok := m[k]; !ok {
			t.Errorf("no key %s", k)
		}
	}
	if string(m["build_stamp"]) != `"claude 2.1.296"` {
		t.Errorf("build_stamp = %s", m["build_stamp"])
	}
}

// c3 gates whether hooks may be the turn source: every prompt must show exactly
// one UserPromptSubmit and one Stop or StopFailure.
func TestC3OneSubmitAndOneStopPerPrompt(t *testing.T) {
	good := []Line{
		mark(1*ms, "prompt-sent", "p1"), hook(2*ms, "UserPromptSubmit"), hook(9*ms, "Stop"), mark(10*ms, "prompt-done", "p1"),
		mark(11*ms, "prompt-sent", "p2"), hook(12*ms, "UserPromptSubmit"), hook(19*ms, "StopFailure"), mark(20*ms, "prompt-done", "p2"),
	}
	for name, tc := range map[string]struct {
		lines []Line
		want  string
	}{
		"one each, a Stop then a StopFailure": {good, StatusPass},
		"two submits for one prompt": {[]Line{
			mark(1*ms, "prompt-sent", "p1"), hook(2*ms, "UserPromptSubmit"), hook(3*ms, "UserPromptSubmit"), hook(9*ms, "Stop"), mark(10*ms, "prompt-done", "p1"),
		}, StatusFail},
		"no stop": {[]Line{
			mark(1*ms, "prompt-sent", "p1"), hook(2*ms, "UserPromptSubmit"), mark(10*ms, "prompt-done", "p1"),
		}, StatusFail},
		"a Stop and a StopFailure for one prompt": {[]Line{
			mark(1*ms, "prompt-sent", "p1"), hook(2*ms, "UserPromptSubmit"), hook(8*ms, "Stop"), hook(9*ms, "StopFailure"), mark(10*ms, "prompt-done", "p1"),
		}, StatusFail},
		"no submit": {[]Line{
			mark(1*ms, "prompt-sent", "p1"), hook(9*ms, "Stop"), mark(10*ms, "prompt-done", "p1"),
		}, StatusFail},
		"a prompt that never finished": {[]Line{
			mark(1*ms, "prompt-sent", "p1"), hook(2*ms, "UserPromptSubmit"), hook(9*ms, "Stop"),
		}, StatusFail},
		"no prompts at all": {[]Line{hook(2*ms, "Stop")}, StatusNotRun},
	} {
		if got := get(t, Run(tc.lines, "s"), "c3").Status; got != tc.want {
			t.Errorf("%s: c3 = %q, want %q", name, got, tc.want)
		}
	}
	// A hook outside every prompt window does not count against a prompt.
	stray := append([]Line{hook(0, "Stop")}, good...)
	if got := get(t, Run(stray, "s"), "c3").Status; got != StatusPass {
		t.Errorf("a stray Stop before the first prompt made c3 %q", got)
	}
}

func TestC1HooksAtTheTrustDialogAndTheSetupMenu(t *testing.T) {
	r := Run([]Line{mark(1*ms, "trust-open", ""), hook(2*ms, "SessionStart"), hook(3*ms, "Notification"), mark(4*ms, "trust-closed", ""), hook(5*ms, "SessionStart")}, "s")
	c := get(t, r, "c1")
	if c.Status != StatusObserved || c.Value != 2 {
		t.Errorf("c1 = %+v, want observed with 2 hooks inside the dialog", c)
	}
	// The setup menu follows the trust dialog; its hooks count when it is marked.
	withSetup := []Line{mark(1*ms, "trust-open", ""), hook(2*ms, "SessionStart"), mark(4*ms, "trust-closed", ""), hook(5*ms, "Notification"), mark(6*ms, "setup-closed", ""), hook(7*ms, "Stop")}
	if c := get(t, Run(withSetup, "s"), "c1"); c.Value != 2 {
		t.Errorf("c1 with a setup menu = %+v, want 2 hooks up to setup-closed", c)
	}
	if got := get(t, Run([]Line{mark(1*ms, "trust-open", "")}, "s"), "c1").Status; got != StatusNotRun {
		t.Errorf("an unclosed dialog window gave c1 %q", got)
	}
}

func TestC2CostMovementWhileIdle(t *testing.T) {
	still := []Line{mark(1*ms, "idle-start", ""), status(2*ms, "0.10", 500), status(3*ms, "0.10", 500), mark(4*ms, "idle-end", "")}
	moved := []Line{mark(1*ms, "idle-start", ""), status(2*ms, "0.10", 500), status(3*ms, "0.12", 500), mark(4*ms, "idle-end", "")}
	for name, tc := range map[string]struct {
		lines []Line
		moved bool
	}{"flat": {still, false}, "cost moved": {moved, true}} {
		c := get(t, Run(tc.lines, "s"), "c2")
		v, _ := c.Value.(map[string]any)
		if c.Status != StatusObserved || v["cost_moved"] != tc.moved || v["samples"] != 2 {
			t.Errorf("%s: c2 = %+v", name, c)
		}
	}
	ctx := []Line{mark(1*ms, "idle-start", ""), status(2*ms, "0.10", 500), status(3*ms, "0.10", 900), mark(4*ms, "idle-end", "")}
	if v, _ := get(t, Run(ctx, "s"), "c2").Value.(map[string]any); v["context_moved"] != true || v["cost_moved"] != false {
		t.Errorf("a context change alone: %+v", v)
	}
}

// c4 is one key: the ring form's source and the submit-to-hook latency in ms.
func TestC4RingFormHoldsTheSourceAndTheLatency(t *testing.T) {
	lines := []Line{
		mark(100*ms, "ring-sent", "wake"),
		hookBody(130*ms, "UserPromptSubmit", `{"hook_event_name":"UserPromptSubmit","prompt":"wake","source":"typed","session_id":"s"}`),
		hook(400*ms, "Stop"), mark(410*ms, "ring-done", ""),
	}
	c := get(t, Run(lines, "s"), "c4")
	v, _ := c.Value.(map[string]any)
	if c.Status != StatusObserved || v["event"] != "UserPromptSubmit" || v["source"] != "typed" || v["prompt_matches_ring"] != true || v["latency_ms"] != int64(30) {
		t.Errorf("c4 = %+v", c)
	}
	none := Run([]Line{mark(100*ms, "ring-sent", "wake"), hook(400*ms, "Stop"), mark(410*ms, "ring-done", "")}, "s")
	if got := get(t, none, "c4").Status; got != StatusFail {
		t.Errorf("a ring that produced no submit: c4 = %q", got)
	}
	if got := get(t, Run(nil, "s"), "c4").Status; got != StatusNotRun {
		t.Errorf("no ring: c4 = %q", got)
	}
}

// c5 is PermissionDenied.reason plus the Notification.notification_type seen.
func TestC5DenialReasonAndNotificationType(t *testing.T) {
	lines := []Line{
		mark(1*ms, "denial-start", ""),
		hookBody(2*ms, "PermissionDenied", `{"hook_event_name":"PermissionDenied","reason":"Permission to use Bash was denied"}`),
		hookBody(3*ms, "Notification", `{"hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission"}`),
		hookBody(4*ms, "Notification", `{"hook_event_name":"Notification","notification_type":"idle_prompt"}`),
		hookBody(5*ms, "Notification", `{"hook_event_name":"Notification","notification_type":"permission_prompt"}`),
		hook(6*ms, "Stop"),
		mark(7*ms, "denial-end", ""),
	}
	c := get(t, Run(lines, "s"), "c5")
	v, _ := c.Value.(map[string]any)
	types, _ := v["notification_types"].([]string)
	if c.Status != StatusObserved || v["reason"] != "Permission to use Bash was denied" || len(types) != 2 || types[0] != "permission_prompt" || types[1] != "idle_prompt" {
		t.Errorf("c5 = %+v", c)
	}
	none := []Line{mark(1*ms, "denial-start", ""), hook(2*ms, "Stop"), mark(3*ms, "denial-end", "")}
	if v, _ := get(t, Run(none, "s"), "c5").Value.(map[string]any); v["reason"] != "" {
		t.Errorf("a window with no denial: %+v", v)
	}
}

func TestC6InterruptEvent(t *testing.T) {
	for name, tc := range map[string]struct {
		hook string
		want string
	}{"a Stop": {"Stop", "stop"}, "a StopFailure": {"StopFailure", "stopfailure"}, "nothing": {"", "none"}} {
		lines := []Line{mark(1*ms, "interrupt-sent", "")}
		if tc.hook != "" {
			lines = append(lines, hook(5*ms, tc.hook))
		}
		lines = append(lines, mark(9*ms, "interrupt-end", ""))
		if c := get(t, Run(lines, "s"), "c6"); c.Status != StatusObserved || c.Value != tc.want {
			t.Errorf("%s: c6 = %+v, want %q", name, c, tc.want)
		}
	}
	if got := get(t, Run(nil, "s"), "c6").Status; got != StatusNotRun {
		t.Errorf("no interrupt marks: c6 = %q", got)
	}
}

func TestC7MovementWithNoPrompt(t *testing.T) {
	quiet := []Line{mark(1*ms, "quiet-start", ""), status(2*ms, "0.10", 500), status(3*ms, "0.10", 500), mark(4*ms, "quiet-end", "")}
	if c := get(t, Run(quiet, "s"), "c7"); c.Status != StatusPass {
		t.Errorf("a quiet window with no turn hook: c7 = %+v", c)
	}
	// A statusline change alone is movement for the report, and is recorded, but
	// it is not a turn hook, so it does not fail the check.
	moved := []Line{mark(1*ms, "quiet-start", ""), status(2*ms, "0.10", 500), status(3*ms, "0.20", 700), mark(4*ms, "quiet-end", "")}
	c := get(t, Run(moved, "s"), "c7")
	v, _ := c.Value.(map[string]any)
	if c.Status != StatusPass || v["statusline_changes"] != 1 || v["turn_hooks"] != 0 {
		t.Errorf("c7 with a statusline change = %+v", c)
	}
	turn := []Line{mark(1*ms, "quiet-start", ""), hook(2*ms, "UserPromptSubmit"), mark(4*ms, "quiet-end", "")}
	if c := get(t, Run(turn, "s"), "c7"); c.Status != StatusFail {
		t.Errorf("a UserPromptSubmit with no prompt: c7 = %+v", c)
	}
}

// c8 is the seconds until the idle_prompt notification, or "never", measured
// from when claude went idle: the later of setup-closed and the last Stop or
// StopFailure before the wait window opens, or the window's own start when
// neither is marked.
func TestC8IdleNotificationSeconds(t *testing.T) {
	const s = int64(1_000_000_000)
	idle := func(ns int64) Line {
		return hookBody(ns, "Notification", `{"hook_event_name":"Notification","notification_type":"idle_prompt"}`)
	}
	other := func(ns int64) Line {
		return hookBody(ns, "Notification", `{"hook_event_name":"Notification","notification_type":"permission_prompt"}`)
	}
	seconds := func(lines []Line) Check { return get(t, Run(lines, "x"), "c8") }

	// A non-idle Notification inside the window is not the idle prompt, and the
	// first idle_prompt is the one counted.
	c := seconds([]Line{mark(1*s, "idle-wait-start", ""), other(30 * s), idle(61*s + 500*ms), idle(90 * s), mark(120*s, "idle-wait-end", "")})
	if c.Status != StatusObserved || c.Value != 60.5 {
		t.Errorf("c8 = %+v, want 60.5 s from the first idle_prompt, past the permission_prompt at 30 s", c)
	}
	never := seconds([]Line{mark(1*s, "idle-wait-start", ""), other(30 * s), mark(120*s, "idle-wait-end", "")})
	if never.Status != StatusObserved || never.Value != "never" {
		t.Errorf("only a permission_prompt: c8 = %+v, want \"never\"", never)
	}
	outside := seconds([]Line{mark(1*s, "idle-wait-start", ""), mark(120*s, "idle-wait-end", ""), idle(130 * s)})
	if outside.Value != "never" {
		t.Errorf("an idle_prompt after the window: c8 = %+v", outside)
	}
	if got := get(t, Run(nil, "x"), "c8").Status; got != StatusNotRun {
		t.Errorf("no window: c8 = %q", got)
	}

	// Claude was already idle when the window opened: the clock starts at
	// setup-closed, and an idle_prompt before the window counts.
	late := seconds([]Line{mark(1*s, "setup-closed", ""), mark(61*s, "idle-wait-start", ""), idle(65 * s), mark(181*s, "idle-wait-end", "")})
	if late.Value != 64.0 || !strings.Contains(late.Note, "setup-closed") {
		t.Errorf("a window opened 60 s after setup: c8 = %+v, want 64 s from setup-closed", late)
	}
	before := seconds([]Line{mark(1*s, "setup-closed", ""), idle(50 * s), mark(61*s, "idle-wait-start", ""), mark(181*s, "idle-wait-end", "")})
	if before.Value != 49.0 {
		t.Errorf("an idle_prompt during the earlier idle: c8 = %+v, want 49 s, not never", before)
	}

	// After a turn, idle starts at the last Stop.
	afterStop := seconds([]Line{mark(1*s, "setup-closed", ""), hook(100*s, "Stop"), mark(120*s, "idle-wait-start", ""), idle(170 * s), mark(300*s, "idle-wait-end", "")})
	if afterStop.Value != 70.0 || !strings.Contains(afterStop.Note, "Stop") {
		t.Errorf("after a Stop at 100 s: c8 = %+v, want 70 s from the Stop", afterStop)
	}
	failed := seconds([]Line{mark(1*s, "setup-closed", ""), hook(100*s, "StopFailure"), mark(120*s, "idle-wait-start", ""), idle(170 * s), mark(300*s, "idle-wait-end", "")})
	if failed.Value != 70.0 {
		t.Errorf("after a StopFailure: c8 = %+v", failed)
	}
	// A Stop after the window opened is activity inside it, not the idle start.
	inside := seconds([]Line{mark(1*s, "setup-closed", ""), mark(10*s, "idle-wait-start", ""), hook(20*s, "Stop"), idle(70 * s), mark(300*s, "idle-wait-end", "")})
	if inside.Value != 69.0 {
		t.Errorf("a Stop inside the window: c8 = %+v, want 69 s from setup-closed", inside)
	}
	// With neither mark, the window's own start is all there is.
	bare := seconds([]Line{mark(5*s, "idle-wait-start", ""), idle(35 * s), mark(60*s, "idle-wait-end", "")})
	if bare.Value != 30.0 {
		t.Errorf("no setup-closed and no Stop: c8 = %+v, want 30 s", bare)
	}
}

func TestC9HookStdoutReachesTheContext(t *testing.T) {
	for body, want := range map[string]bool{"yes": true, "no": false} {
		c := get(t, Run([]Line{mark(1*ms, "stdout-canary", body)}, "s"), "c9")
		if c.Status != StatusObserved || c.Value != want {
			t.Errorf("canary %q: c9 = %+v", body, c)
		}
	}
	if c := get(t, Run([]Line{mark(1*ms, "stdout-canary", "maybe")}, "s"), "c9"); c.Status != StatusFail {
		t.Errorf("an unreadable answer: c9 = %+v", c)
	}
}

// c10 is an exit 2 only: what a blocking exit does to the prompt.
func TestC10HookExit2Effect(t *testing.T) {
	c := get(t, Run([]Line{mark(2*ms, "exit2-effect", "blocked")}, "s"), "c10")
	if c.Status != StatusObserved || c.Value != "blocked" {
		t.Errorf("c10 = %+v", c)
	}
	if got := get(t, Run([]Line{mark(1*ms, "exit1-effect", "ran on")}, "s"), "c10").Status; got != StatusNotRun {
		t.Errorf("an exit 1 alone is not c10: %q", got)
	}
}

func TestC11SubagentStamps(t *testing.T) {
	lines := []Line{
		mark(1*ms, "subagent-start", ""),
		hook(2*ms, "UserPromptSubmit"),
		hookBody(3*ms, "SubagentStop", `{"hook_event_name":"SubagentStop","agent_id":"a1"}`),
		hookBody(4*ms, "SubagentStop", `{"hook_event_name":"SubagentStop"}`),
		hook(5*ms, "Stop"),
		mark(6*ms, "subagent-end", ""),
	}
	c := get(t, Run(lines, "s"), "c11")
	v, _ := c.Value.(map[string]any)
	if c.Status != StatusObserved || v["subagent_events"] != 2 || v["with_agent_id"] != 1 || v["turn_stops"] != 1 {
		t.Errorf("c11 = %+v", c)
	}
}

func TestC12StopBackground(t *testing.T) {
	early := []Line{mark(1*ms, "bg-start", ""), hook(2*ms, "UserPromptSubmit"), hook(5*ms, "Stop"), mark(8*ms, "bg-task-done", ""), mark(9*ms, "bg-end", "")}
	late := []Line{mark(1*ms, "bg-start", ""), hook(2*ms, "UserPromptSubmit"), mark(5*ms, "bg-task-done", ""), hook(8*ms, "Stop"), mark(9*ms, "bg-end", "")}
	for name, tc := range map[string]struct {
		lines []Line
		want  bool
	}{"a Stop while the task still ran": {early, true}, "a Stop after the task": {late, false}} {
		c := get(t, Run(tc.lines, "s"), "c12")
		v, _ := c.Value.(map[string]any)
		if c.Status != StatusObserved || v["stop_before_task_done"] != tc.want {
			t.Errorf("%s: c12 = %+v", name, c)
		}
	}
}

func TestC13VersionVisibleToAHook(t *testing.T) {
	inPayload := []Line{hookBody(1*ms, "SessionStart", `{"hook_event_name":"SessionStart","version":"2.1.296"}`)}
	inEnv := []Line{{MonoNS: 1 * ms, Kind: KindHookEnv, Name: "SessionStart", Body: "CLAUDECODE CLAUDE_CODE_VERSION=2.1.296"}}
	neither := []Line{hook(1*ms, "SessionStart"), {MonoNS: 2 * ms, Kind: KindHookEnv, Name: "SessionStart", Body: "CLAUDECODE"}}
	for name, tc := range map[string]struct {
		lines   []Line
		payload bool
		env     string
	}{"in the payload": {inPayload, true, ""}, "in the environment": {inEnv, false, "CLAUDE_CODE_VERSION=2.1.296"}, "nowhere": {neither, false, ""}} {
		c := get(t, Run(tc.lines, "s"), "c13")
		v, _ := c.Value.(map[string]any)
		if c.Status != StatusObserved || v["payload_field"] != tc.payload || v["env"] != tc.env {
			t.Errorf("%s: c13 = %+v", name, c)
		}
	}
}
