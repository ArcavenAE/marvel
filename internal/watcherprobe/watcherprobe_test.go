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
	out, err := RecordStatusline(path, strings.NewReader(`{"cost":{"total_cost_usd":0.5}}`))
	if err != nil || out == "" || strings.Contains(out, "\n") {
		t.Fatalf("printed %q, %v; want one line", out, err)
	}
	raw, _ := os.ReadFile(path)
	lines, err := Parse(bytes.NewReader(raw))
	if err != nil || len(lines) != 1 || lines[0].Kind != KindStatus || !strings.Contains(lines[0].Body, "0.5") {
		t.Errorf("log = %+v, %v", lines, err)
	}
}

func TestCheckWithNothingLoggedIsNotRun(t *testing.T) {
	r := Run(nil, "claude 2.1.296")
	if len(r.Checks) != 13 {
		t.Fatalf("%d checks, want 13", len(r.Checks))
	}
	for i := 1; i <= 13; i++ {
		if c := get(t, r, fmt.Sprintf("c%d", i)); c.Status != StatusNotRun {
			t.Errorf("c%d = %q with an empty log, want %q", i, c.Status, StatusNotRun)
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

func TestC1HooksAtTheTrustDialog(t *testing.T) {
	r := Run([]Line{mark(1*ms, "trust-open", ""), hook(2*ms, "SessionStart"), hook(3*ms, "Notification"), mark(4*ms, "trust-closed", ""), hook(5*ms, "SessionStart")}, "s")
	c := get(t, r, "c1")
	if c.Status != StatusObserved || c.Value != 2 {
		t.Errorf("c1 = %+v, want observed with 2 hooks inside the dialog", c)
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

func TestC4AndC5TheRingFormAndItsLatency(t *testing.T) {
	lines := []Line{
		mark(100*ms, "ring-sent", "wake"),
		hookBody(130*ms, "UserPromptSubmit", `{"hook_event_name":"UserPromptSubmit","prompt":"wake","session_id":"s"}`),
		hook(400*ms, "Stop"), mark(410*ms, "ring-done", ""),
	}
	r := Run(lines, "s")
	c4, c5 := get(t, r, "c4"), get(t, r, "c5")
	v, _ := c4.Value.(map[string]any)
	if c4.Status != StatusObserved || v["event"] != "UserPromptSubmit" || v["prompt_matches_ring"] != true {
		t.Errorf("c4 = %+v", c4)
	}
	if c5.Status != StatusObserved || c5.Value != int64(30) {
		t.Errorf("c5 = %+v, want 30 ms", c5)
	}
	none := Run([]Line{mark(100*ms, "ring-sent", "wake"), hook(400*ms, "Stop"), mark(410*ms, "ring-done", "")}, "s")
	if get(t, none, "c4").Status != StatusFail || get(t, none, "c5").Status != StatusFail {
		t.Errorf("a ring that produced no submit: c4 %+v c5 %+v", get(t, none, "c4"), get(t, none, "c5"))
	}
}

func TestC6DenialText(t *testing.T) {
	lines := []Line{
		mark(1*ms, "denial-start", ""),
		hookBody(2*ms, "PermissionDenied", `{"hook_event_name":"PermissionDenied","message":"Permission to use Bash was denied"}`),
		hookBody(3*ms, "Notification", `{"hook_event_name":"Notification","message":"Claude needs your permission"}`),
		hook(4*ms, "Stop"),
		mark(5*ms, "denial-end", ""),
	}
	c := get(t, Run(lines, "s"), "c6")
	texts, _ := c.Value.([]string)
	if c.Status != StatusObserved || len(texts) != 2 || texts[0] != "Permission to use Bash was denied" {
		t.Errorf("c6 = %+v", c)
	}
}

func TestC7InterruptBehavior(t *testing.T) {
	lines := []Line{mark(1*ms, "interrupt-sent", ""), hook(5*ms, "Stop"), mark(9*ms, "interrupt-end", "")}
	c := get(t, Run(lines, "s"), "c7")
	v, _ := c.Value.(map[string]any)
	if c.Status != StatusObserved || v["stops"] != 1 || v["failures"] != 0 {
		t.Errorf("c7 = %+v", c)
	}
	lines = []Line{mark(1*ms, "interrupt-sent", ""), hook(5*ms, "StopFailure"), mark(9*ms, "interrupt-end", "")}
	if v, _ := get(t, Run(lines, "s"), "c7").Value.(map[string]any); v["failures"] != 1 || v["stops"] != 0 {
		t.Errorf("an interrupt that ended in StopFailure: %+v", v)
	}
	lines = []Line{mark(1*ms, "interrupt-sent", ""), mark(9*ms, "interrupt-end", "")}
	if v, _ := get(t, Run(lines, "s"), "c7").Value.(map[string]any); v["stops"] != 0 || v["failures"] != 0 {
		t.Errorf("an interrupt that produced no stop: %+v", v)
	}
}

func TestC8MovementWithNoPrompt(t *testing.T) {
	quiet := []Line{mark(1*ms, "quiet-start", ""), status(2*ms, "0.10", 500), status(3*ms, "0.10", 500), mark(4*ms, "quiet-end", "")}
	if c := get(t, Run(quiet, "s"), "c8"); c.Status != StatusPass {
		t.Errorf("a quiet window with no turn hook: c8 = %+v", c)
	}
	// A statusline change alone is movement for the report, and is recorded, but
	// it is not a turn hook, so it does not fail the check.
	moved := []Line{mark(1*ms, "quiet-start", ""), status(2*ms, "0.10", 500), status(3*ms, "0.20", 700), mark(4*ms, "quiet-end", "")}
	c := get(t, Run(moved, "s"), "c8")
	v, _ := c.Value.(map[string]any)
	if c.Status != StatusPass || v["statusline_changes"] != 1 || v["turn_hooks"] != 0 {
		t.Errorf("c8 with a statusline change = %+v", c)
	}
	turn := []Line{mark(1*ms, "quiet-start", ""), hook(2*ms, "UserPromptSubmit"), mark(4*ms, "quiet-end", "")}
	if c := get(t, Run(turn, "s"), "c8"); c.Status != StatusFail {
		t.Errorf("a UserPromptSubmit with no prompt: c8 = %+v", c)
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

func TestC10ANonzeroExitsEffect(t *testing.T) {
	r := Run([]Line{mark(1*ms, "exit1-effect", "ran on"), mark(2*ms, "exit2-effect", "blocked")}, "s")
	c := get(t, r, "c10")
	v, _ := c.Value.(map[string]any)
	if c.Status != StatusObserved || v["exit1"] != "ran on" || v["exit2"] != "blocked" {
		t.Errorf("c10 = %+v", c)
	}
	if got := get(t, Run([]Line{mark(1*ms, "exit1-effect", "ran on")}, "s"), "c10").Status; got != StatusNotRun {
		t.Errorf("only one exit measured: c10 = %q", got)
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

func TestC12BackgroundTasks(t *testing.T) {
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

func TestC13CanAHookSeeTheVersion(t *testing.T) {
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
