package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/runtime/events"
)

// fixedClock returns a stable time so parser output is deterministic in
// tests. Not exported — every test constructs one inline.
func fixedClock() func() time.Time {
	t := time.Date(2026, 7, 5, 20, 12, 31, 0, time.UTC)
	return func() time.Time {
		t = t.Add(time.Millisecond)
		return t
	}
}

// collectFixture runs the parser over a testdata fixture and returns
// every emitted event.
func collectFixture(t *testing.T, name string) []events.Event {
	t.Helper()
	path := filepath.Join("testdata", name)
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { _ = f.Close() })

	p := NewParser(Config{
		AgentID:   "reviewer-a",
		Workspace: "aae-orc",
		Clock:     fixedClock(),
	})
	var out []events.Event
	if err := p.Parse(context.Background(), f, func(e events.Event) {
		out = append(out, e)
	}); err != nil {
		t.Fatalf("Parse %s: %v", path, err)
	}
	return out
}

func TestParseHelloFixture(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "hello.ndjson")

	// hello fixture: system/init + assistant(text) + result.
	// Expected: session.started + message.completed + session.ended.
	wantKinds := []events.Kind{
		events.KindSessionStarted,
		events.KindMessageCompleted,
		events.KindSessionEnded,
	}
	if len(got) != len(wantKinds) {
		t.Fatalf("event count = %d, want %d — events: %+v", len(got), len(wantKinds), kinds(got))
	}
	for i, ev := range got {
		if ev.Event != wantKinds[i] {
			t.Errorf("event[%d].kind = %s, want %s", i, ev.Event, wantKinds[i])
		}
	}

	// session.started data — model + cwd + tools should have flowed through.
	started, ok := got[0].Data.(events.SessionStartedData)
	if !ok {
		t.Fatalf("event[0].Data type = %T, want SessionStartedData", got[0].Data)
	}
	if started.Model == "" {
		t.Error("session.started missing model")
	}
	if started.Cwd == "" {
		t.Error("session.started missing cwd")
	}
	if len(started.Tools) == 0 {
		t.Error("session.started missing tools list")
	}
	if started.Resumed {
		t.Error("session.started marked resumed=true on fresh session")
	}

	// message.completed — role must be assistant, text must be non-empty.
	msg, ok := got[1].Data.(events.MessageData)
	if !ok {
		t.Fatalf("event[1].Data type = %T, want MessageData", got[1].Data)
	}
	if msg.Role != "assistant" {
		t.Errorf("message.completed role = %q, want assistant", msg.Role)
	}
	if msg.Text == "" {
		t.Error("message.completed missing text")
	}

	// session.ended — reason should map from stop_reason, exit_code 0,
	// usage populated.
	ended, ok := got[2].Data.(events.SessionEndedData)
	if !ok {
		t.Fatalf("event[2].Data type = %T, want SessionEndedData", got[2].Data)
	}
	if ended.Reason == "" {
		t.Error("session.ended missing reason")
	}
	if ended.ExitCode != 0 {
		t.Errorf("session.ended exit_code = %d, want 0", ended.ExitCode)
	}
	if ended.Usage.In == 0 {
		t.Error("session.ended usage.in = 0")
	}
	if ended.Usage.Cost == nil || *ended.Usage.Cost == 0 {
		t.Error("session.ended usage.cost unset")
	}
}

func TestParseHelloSessionIDPropagation(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "hello.ndjson")

	// Every event after system/init should carry the discovered
	// harness_session_id.
	if len(got) < 3 {
		t.Fatalf("expected 3+ events, got %d", len(got))
	}
	first := got[0].HarnessSessionID
	if first == "" {
		t.Fatal("session.started missing harness_session_id")
	}
	for i, ev := range got {
		if ev.HarnessSessionID != first {
			t.Errorf("event[%d] session_id = %q, want %q", i, ev.HarnessSessionID, first)
		}
	}
}

func TestParseHelloAgentAndWorkspaceStamped(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "hello.ndjson")
	for i, ev := range got {
		if ev.AgentID != "reviewer-a" {
			t.Errorf("event[%d].agent_id = %q, want reviewer-a", i, ev.AgentID)
		}
		if ev.Workspace != "aae-orc" {
			t.Errorf("event[%d].workspace = %q, want aae-orc", i, ev.Workspace)
		}
		if ev.Harness != Harness {
			t.Errorf("event[%d].harness = %q, want %q", i, ev.Harness, Harness)
		}
		if ev.SchemaVersion != events.SchemaVersion {
			t.Errorf("event[%d].schema_version = %d, want %d", i, ev.SchemaVersion, events.SchemaVersion)
		}
	}
}

func TestParseSeqMonotonic(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "hello.ndjson")
	if len(got) == 0 {
		t.Fatal("no events")
	}
	for i, ev := range got {
		want := uint64(i + 1)
		if ev.Seq != want {
			t.Errorf("event[%d].seq = %d, want %d", i, ev.Seq, want)
		}
	}
}

func TestParseToolCallFixture(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "tool_call.ndjson")

	// tool_call fixture (10 vendor lines):
	//   1  system/init                    → session.started
	//   2  assistant [thinking]           → error{unmapped}  (thinking not in v1)
	//   3  assistant [tool_use]           → tool.call
	//   4  user      [tool_result]        → tool.result
	//   5  assistant [thinking]           → error{unmapped}
	//   6  assistant [text]               → message.completed
	//   7  assistant [tool_use]           → tool.call
	//   8  user      [tool_result]        → tool.result
	//   9  assistant [text]               → message.completed
	//  10  result                         → session.ended
	//
	// Total: 10 events (1:1 with vendor lines because each line
	// carries exactly one content block).
	wantKinds := []events.Kind{
		events.KindSessionStarted,
		events.KindError,
		events.KindToolCall,
		events.KindToolResult,
		events.KindError,
		events.KindMessageCompleted,
		events.KindToolCall,
		events.KindToolResult,
		events.KindMessageCompleted,
		events.KindSessionEnded,
	}
	if len(got) != len(wantKinds) {
		t.Fatalf("event count = %d, want %d — kinds: %v", len(got), len(wantKinds), kinds(got))
	}
	for i, ev := range got {
		if ev.Event != wantKinds[i] {
			t.Errorf("event[%d].kind = %s, want %s", i, ev.Event, wantKinds[i])
		}
	}
}

func TestParseToolCallCorrelation(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "tool_call.ndjson")

	// tool.call and the next tool.result must share call_id.
	var lastCallID string
	for _, ev := range got {
		switch ev.Event {
		case events.KindToolCall:
			d := ev.Data.(events.ToolCallData)
			if d.CallID == "" {
				t.Error("tool.call missing call_id")
			}
			if d.Tool == "" {
				t.Error("tool.call missing tool name")
			}
			lastCallID = d.CallID
		case events.KindToolResult:
			d := ev.Data.(events.ToolResultData)
			if d.CallID != lastCallID {
				t.Errorf("tool.result call_id = %q, want %q", d.CallID, lastCallID)
			}
		}
	}
}

func TestParseToolCallErrorFlag(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "tool_call.ndjson")

	// The first tool_result in the fixture is is_error=true (Read of
	// /etc/hostname failed on macOS); the second is is_error=false.
	// After mapping: first ToolResultData.OK = false, second = true.
	var results []events.ToolResultData
	for _, ev := range got {
		if ev.Event == events.KindToolResult {
			results = append(results, ev.Data.(events.ToolResultData))
		}
	}
	if len(results) < 2 {
		t.Fatalf("expected 2 tool.result events, got %d", len(results))
	}
	if results[0].OK {
		t.Error("first tool.result should be ok=false (Read failure)")
	}
	if !results[1].OK {
		t.Error("second tool.result should be ok=true (Bash success)")
	}
}

func TestParseUnmappedPreservesRaw(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "tool_call.ndjson")

	// Every error{unmapped} event MUST carry raw so nothing is dropped
	// on the floor per §3.2.
	for i, ev := range got {
		if ev.Event != events.KindError {
			continue
		}
		d, ok := ev.Data.(events.ErrorData)
		if !ok {
			t.Fatalf("event[%d].Data = %T, want ErrorData", i, ev.Data)
		}
		if d.Kind != events.ErrKindUnmapped {
			continue
		}
		if len(ev.Raw) == 0 {
			t.Errorf("event[%d] unmapped error missing raw", i)
		}
		// Raw must be valid JSON — the original vendor line, preserved.
		var check any
		if err := json.Unmarshal(ev.Raw, &check); err != nil {
			t.Errorf("event[%d] raw not valid JSON: %v", i, err)
		}
	}
}

func TestParseMalformedLineEmitsParseError(t *testing.T) {
	t.Parallel()
	// Well-formed system/init followed by garbage followed by a valid
	// result. Parser must emit an error{parse} for the middle line and
	// keep going — one bad line does not stop the stream.
	stream := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"s1","model":"m","cwd":"/tmp"}`,
		`{not valid json at all}`,
		`{"type":"result","subtype":"success","session_id":"s1","stop_reason":"end_turn","is_error":false,"usage":{"input_tokens":1,"output_tokens":2},"total_cost_usd":0.01}`,
	}, "\n")

	p := NewParser(Config{Clock: fixedClock()})
	var got []events.Event
	if err := p.Parse(context.Background(), strings.NewReader(stream), func(e events.Event) {
		got = append(got, e)
	}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("event count = %d, want 3 (start + parse-error + end): %v", len(got), kinds(got))
	}
	if got[0].Event != events.KindSessionStarted {
		t.Errorf("got[0] = %s, want session.started", got[0].Event)
	}
	if got[1].Event != events.KindError {
		t.Errorf("got[1] = %s, want error", got[1].Event)
	} else if d := got[1].Data.(events.ErrorData); d.Kind != events.ErrKindParse {
		t.Errorf("got[1].kind = %q, want parse", d.Kind)
	}
	if got[2].Event != events.KindSessionEnded {
		t.Errorf("got[2] = %s, want session.ended", got[2].Event)
	}
}

func TestParseUnknownTopLevelType(t *testing.T) {
	t.Parallel()
	stream := `{"type":"future-vendor-event","session_id":"s1","foo":"bar"}`
	p := NewParser(Config{Clock: fixedClock()})
	var got []events.Event
	if err := p.Parse(context.Background(), strings.NewReader(stream), func(e events.Event) {
		got = append(got, e)
	}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("event count = %d, want 1", len(got))
	}
	if got[0].Event != events.KindError {
		t.Fatalf("kind = %s, want error", got[0].Event)
	}
	d := got[0].Data.(events.ErrorData)
	if d.Kind != events.ErrKindUnmapped {
		t.Errorf("error kind = %q, want unmapped", d.Kind)
	}
	if len(got[0].Raw) == 0 {
		t.Error("unmapped raw missing")
	}
	// harness_session_id was learned before the unknown type dispatch.
	if got[0].HarnessSessionID != "s1" {
		t.Errorf("session_id = %q, want s1", got[0].HarnessSessionID)
	}
}

func TestParseEmptyLinesSkipped(t *testing.T) {
	t.Parallel()
	stream := "\n\n" +
		`{"type":"system","subtype":"init","session_id":"s","model":"m","cwd":"/"}` + "\n\n"
	p := NewParser(Config{Clock: fixedClock()})
	var got []events.Event
	if err := p.Parse(context.Background(), strings.NewReader(stream), func(e events.Event) {
		got = append(got, e)
	}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("event count = %d, want 1 (blank lines skipped)", len(got))
	}
}

func TestParseContextCancellation(t *testing.T) {
	t.Parallel()
	stream := `{"type":"system","subtype":"init","session_id":"s","model":"m","cwd":"/"}`
	// Cancel before Parse runs; the scanner still consumes one line
	// (bufio has no cancellation hook), but the loop's ctx check
	// fires on the next iteration. Confirm we return ctx.Err on a
	// pre-cancelled context AND still emit events accumulated up to
	// the cancellation point.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := NewParser(Config{Clock: fixedClock()})
	var got []events.Event
	err := p.Parse(ctx, bytes.NewReader([]byte(stream+"\n"+stream)), func(e events.Event) {
		got = append(got, e)
	})
	// Behavior: at least one iteration may run before the ctx check;
	// the second iteration must observe the cancellation.
	if err == nil {
		t.Fatal("Parse: want ctx.Err, got nil")
	}
	if len(got) > 2 {
		t.Errorf("emitted %d events after cancel, want <=2", len(got))
	}
}

func kinds(evs []events.Event) []events.Kind {
	out := make([]events.Kind, len(evs))
	for i, e := range evs {
		out[i] = e.Event
	}
	return out
}
