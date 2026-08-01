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
	// Expected: session.started, then the assistant line's per-request
	// usage as turn.completed, then message.completed for its text block,
	// then session.ended.
	wantKinds := []events.Kind{
		events.KindSessionStarted,
		events.KindTurnCompleted,
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
	msg, ok := got[2].Data.(events.MessageData)
	if !ok {
		t.Fatalf("event[2].Data type = %T, want MessageData", got[2].Data)
	}
	if msg.Role != "assistant" {
		t.Errorf("message.completed role = %q, want assistant", msg.Role)
	}
	if msg.Text == "" {
		t.Error("message.completed missing text")
	}

	// session.ended — reason should map from stop_reason, exit_code 0,
	// usage populated.
	ended, ok := got[3].Data.(events.SessionEndedData)
	if !ok {
		t.Fatalf("event[3].Data type = %T, want SessionEndedData", got[3].Data)
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

	// tool_call fixture (10 vendor lines). Each line carries one content
	// block, and each assistant line also carries its request's usage,
	// emitted once per message.id rather than once per line. Three
	// distinct ids across six assistant lines, hence three turn.completed.
	//
	//   1  system/init                    → session.started
	//   2  assistant [thinking] id=SQ     → turn.completed, error{unmapped}
	//   3  assistant [tool_use] id=SQ     → tool.call            (id repeats)
	//   4  user      [tool_result]        → tool.result          (usage null)
	//   5  assistant [thinking] id=Uf     → turn.completed, error{unmapped}
	//   6  assistant [text]     id=Uf     → message.completed    (id repeats)
	//   7  assistant [tool_use] id=Uf     → tool.call            (id repeats)
	//   8  user      [tool_result]        → tool.result          (usage null)
	//   9  assistant [text]     id=Bn     → turn.completed, message.completed
	//  10  result                         → session.ended
	wantKinds := []events.Kind{
		events.KindSessionStarted,
		events.KindTurnCompleted,
		events.KindError,
		events.KindToolCall,
		events.KindToolResult,
		events.KindTurnCompleted,
		events.KindError,
		events.KindMessageCompleted,
		events.KindToolCall,
		events.KindToolResult,
		events.KindTurnCompleted,
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

func TestParseResultPromotesMeteringFields(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "hello.ndjson")
	ended, ok := got[len(got)-1].Data.(events.SessionEndedData)
	if !ok {
		t.Fatalf("last event data = %T, want SessionEndedData", got[len(got)-1].Data)
	}
	m := ended.Metering
	if m == nil {
		t.Fatal("session.ended carries no metering")
	}

	// Values are read straight off testdata/hello.ndjson.
	if m.DurationMS != 4816 {
		t.Errorf("duration_ms = %d, want 4816", m.DurationMS)
	}
	if m.APIDurationMS != 6546 {
		t.Errorf("duration_api_ms = %d, want 6546", m.APIDurationMS)
	}
	if m.TTFTMS != 4775 {
		t.Errorf("ttft_ms = %d, want 4775", m.TTFTMS)
	}
	if m.TTFTStreamMS != 4031 {
		t.Errorf("ttft_stream_ms = %d, want 4031", m.TTFTStreamMS)
	}
	if m.NumTurns != 1 {
		t.Errorf("num_turns = %d, want 1", m.NumTurns)
	}
	if m.CacheCreationIn != 22005 {
		t.Errorf("cache_creation_in = %d, want 22005", m.CacheCreationIn)
	}
	if len(m.PermissionDenials) != 0 {
		t.Errorf("permission_denials = %v, want empty", m.PermissionDenials)
	}

	// modelUsage is per-model and camelCase on the wire; the fixture
	// routed across two models in one session.
	if len(m.ModelUsage) != 2 {
		t.Fatalf("model_usage has %d entries, want 2: %+v", len(m.ModelUsage), m.ModelUsage)
	}
	haiku, ok := m.ModelUsage["claude-haiku-4-5-20251001"]
	if !ok {
		t.Fatalf("model_usage missing the haiku entry: %+v", m.ModelUsage)
	}
	if haiku.In != 522 || haiku.Out != 13 {
		t.Errorf("haiku usage = in %d out %d, want 522/13", haiku.In, haiku.Out)
	}
	if haiku.Cost == nil || *haiku.Cost != 0.000587 {
		t.Errorf("haiku cost = %v, want 0.000587", haiku.Cost)
	}
}

func TestParseResultWithoutMeteringLeavesItNil(t *testing.T) {
	t.Parallel()
	// A harness that reports none of the accounting fields must be
	// distinguishable from one that reports zeros.
	line := `{"type":"result","subtype":"success","is_error":false,"session_id":"s","usage":{"input_tokens":1,"output_tokens":1}}`
	p := NewParser(Config{Clock: fixedClock()})
	var got []events.Event
	if err := p.Parse(context.Background(), strings.NewReader(line+"\n"), func(e events.Event) {
		got = append(got, e)
	}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	ended, ok := got[0].Data.(events.SessionEndedData)
	if !ok {
		t.Fatalf("data = %T, want SessionEndedData", got[0].Data)
	}
	if ended.Metering != nil {
		t.Errorf("metering = %+v, want nil", ended.Metering)
	}
}

func TestSessionEndedMeteringOmittedFromJSONWhenAbsent(t *testing.T) {
	t.Parallel()
	// Metering is additive under schema_version 1, so an event without
	// it must serialize exactly as it did before the field existed.
	b, err := json.Marshal(events.SessionEndedData{ExitCode: 0})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(b, []byte("metering")) {
		t.Errorf("metering leaked into JSON: %s", b)
	}
}

// TestParseAssistantOccupancyIsALevelNotASum is the regression guard for
// the load-bearing correction in this slice: context occupancy is a LEVEL
// taken per API request, latest-wins, never a sum over requests.
//
// The result line's usage is a session-cumulative total. On this fixture
// its occupancy sum is 100994, while real final occupancy is 34136. A
// numerator built from the result line therefore reads 10.1% against a 1M
// window where the truth is 3.4%, and the error grows with request count.
func TestParseAssistantOccupancyIsALevelNotASum(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "tool_call.ndjson")

	var reqs []*events.RequestUsage
	for i, ev := range got {
		if ev.Event != events.KindTurnCompleted {
			continue
		}
		d, ok := ev.Data.(events.TurnData)
		if !ok {
			t.Fatalf("event[%d].Data = %T, want TurnData", i, ev.Data)
		}
		if d.Request == nil {
			t.Fatalf("event[%d] turn.completed carries no Request", i)
		}
		reqs = append(reqs, d.Request)
	}

	// Six assistant lines, three distinct message ids: the dedupe proof.
	if len(reqs) != 3 {
		t.Fatalf("got %d per-request usage events, want 3 (six assistant lines, three ids)", len(reqs))
	}

	wantOccupancy := []int{33377, 33481, 34136}
	for i, r := range reqs {
		if got := r.Occupancy(); got != wantOccupancy[i] {
			t.Errorf("request[%d] occupancy = %d, want %d", i, got, wantOccupancy[i])
		}
		if r.Layout != events.LayoutAdditive {
			t.Errorf("request[%d] layout = %q, want additive", i, r.Layout)
		}
		// message.model omits the [1m] suffix the init model carries.
		if r.Model != "claude-fable-5" {
			t.Errorf("request[%d] model = %q, want claude-fable-5", i, r.Model)
		}
		if r.RequestID == "" {
			t.Errorf("request[%d] carries no request id", i)
		}
		// Claude publishes no per-request total, so the mismatch
		// invariant must stay disabled for this harness.
		if r.Total != 0 {
			t.Errorf("request[%d] total = %d, want 0", i, r.Total)
		}
	}

	// The trap: the result line's classes sum to 100994 across the three
	// requests. No emitted occupancy may equal it.
	for i, r := range reqs {
		if r.Occupancy() == 100994 {
			t.Errorf("request[%d] occupancy is the result-line sum (100994), not a level", i)
		}
	}
}

// TestParseSingleRequestSessionAgrees documents why the level-vs-sum bug
// hid for so long: in a single-request session the cumulative total and
// the final level are the same number, so every one-shot fixture and every
// single-request live measurement agrees with the wrong formula.
func TestParseSingleRequestSessionAgrees(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "hello.ndjson")

	var req *events.RequestUsage
	for _, ev := range got {
		if ev.Event == events.KindTurnCompleted {
			req = ev.Data.(events.TurnData).Request
		}
	}
	if req == nil {
		t.Fatal("no per-request usage in the hello fixture")
	}
	// 11368 + 0 cache_read + 22005 cache_creation.
	if got := req.Occupancy(); got != 33373 {
		t.Errorf("request occupancy = %d, want 33373", got)
	}

	ended := got[len(got)-1].Data.(events.SessionEndedData)
	m := ended.Metering
	if m == nil {
		t.Fatal("session.ended carries no metering")
	}
	resultSum := ended.Usage.In + m.CacheReadIn + m.CacheCreationIn
	if resultSum != req.Occupancy() {
		t.Errorf("single-request session: result sum %d != level %d", resultSum, req.Occupancy())
	}
}

// TestParseResultContextWindowSelectsInitModel guards the map-range
// hazard. Both fixtures route across two models with 5x-different
// windows; ranging the map would pick the wrong one about half the time.
func TestParseResultContextWindowSelectsInitModel(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "tool_call.ndjson")

	started, ok := got[0].Data.(events.SessionStartedData)
	if !ok {
		t.Fatalf("event[0].Data = %T, want SessionStartedData", got[0].Data)
	}
	if started.Model != "claude-fable-5[1m]" {
		t.Fatalf("init model = %q, want claude-fable-5[1m]", started.Model)
	}

	m := got[len(got)-1].Data.(events.SessionEndedData).Metering
	if m == nil {
		t.Fatal("session.ended carries no metering")
	}
	primary, ok := m.ModelUsage[started.Model]
	if !ok {
		t.Fatalf("model_usage has no entry for the init model: %+v", m.ModelUsage)
	}
	if primary.ContextWindow != 1_000_000 {
		t.Errorf("init-model context window = %d, want 1000000", primary.ContextWindow)
	}
	if primary.MaxOutputTokens != 64_000 {
		t.Errorf("init-model max output = %d, want 64000", primary.MaxOutputTokens)
	}

	haiku, ok := m.ModelUsage["claude-haiku-4-5-20251001"]
	if !ok {
		t.Fatalf("model_usage has no haiku entry: %+v", m.ModelUsage)
	}
	if haiku.ContextWindow != 200_000 {
		t.Errorf("haiku context window = %d, want 200000", haiku.ContextWindow)
	}
	// The point of the test: selecting by anything other than the init
	// model would resolve 200000 here, which is wrong by 5x.
	if primary.ContextWindow == haiku.ContextWindow {
		t.Error("fixture no longer exercises the two-window case")
	}
}

// TestParseUserToolResultEmitsNoRequest pins the usage-is-a-pointer
// guard: user/tool_result lines carry "usage": null, and a zero folded
// into the level series would read as a compaction on every tool result.
func TestParseUserToolResultEmitsNoRequest(t *testing.T) {
	t.Parallel()
	line := `{"type":"user","session_id":"s1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}],"usage":null}}`
	p := NewParser(Config{Clock: fixedClock()})
	var got []events.Event
	if err := p.Parse(context.Background(), strings.NewReader(line+"\n"), func(e events.Event) {
		got = append(got, e)
	}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 1 || got[0].Event != events.KindToolResult {
		t.Fatalf("kinds = %v, want [tool.result] only", kinds(got))
	}
}

// TestParseThinkingOnlyLineStillCarriesUsage is the test an
// attach-usage-to-MessageData design would fail: line 2 of the tool_call
// fixture has one content block, `thinking`, which maps to
// error{unmapped} and never to message.completed. Its usage must still
// reach a consumer.
func TestParseThinkingOnlyLineStillCarriesUsage(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "tool_call.ndjson")
	if len(got) < 3 {
		t.Fatalf("got %d events, want at least 3", len(got))
	}
	turn, ok := got[1].Data.(events.TurnData)
	if !ok {
		t.Fatalf("event[1].Data = %T, want TurnData", got[1].Data)
	}
	if turn.Request == nil || turn.Request.Occupancy() != 33377 {
		t.Errorf("thinking-only line lost its usage: %+v", turn.Request)
	}
	if got[2].Event != events.KindError {
		t.Errorf("event[2].kind = %s, want error{unmapped} for the thinking block", got[2].Event)
	}
}

// TestParseSubagentUsageIsMarked: a Task-tool turn accounts against its
// own context window, so its usage must arrive labelled. Unlabelled, it
// would overwrite the session's occupancy level with a much smaller
// number for the length of the tool call. Both shipped fixtures carry
// "parent_tool_use_id":null on every assistant line, which is the field
// this reads; the non-null shape below is synthetic.
func TestParseSubagentUsageIsMarked(t *testing.T) {
	t.Parallel()
	stream := `{"type":"assistant","session_id":"s1","parent_tool_use_id":null,"message":{"id":"msg_main","model":"claude-fable-5","role":"assistant","content":[{"type":"text","text":"delegating"}],"usage":{"input_tokens":331,"output_tokens":1,"cache_read_input_tokens":33479,"cache_creation_input_tokens":326}}}
{"type":"assistant","session_id":"s1","parent_tool_use_id":"toolu_01Sub","message":{"id":"msg_sub","model":"claude-haiku-4-5","role":"assistant","content":[{"type":"text","text":"subagent work"}],"usage":{"input_tokens":900,"output_tokens":40,"cache_read_input_tokens":1200,"cache_creation_input_tokens":0}}}
`
	p := NewParser(Config{Clock: fixedClock()})
	var reqs []*events.RequestUsage
	if err := p.Parse(context.Background(), strings.NewReader(stream), func(e events.Event) {
		if e.Event == events.KindTurnCompleted {
			reqs = append(reqs, e.Data.(events.TurnData).Request)
		}
	}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(reqs) != 2 {
		t.Fatalf("got %d per-request usage events, want 2", len(reqs))
	}
	if reqs[0].Subagent() {
		t.Errorf("main-agent line marked as a subagent: %q", reqs[0].ParentToolUseID)
	}
	if !reqs[1].Subagent() {
		t.Error("subagent line lost its parent_tool_use_id, so its tokens would land in the session's occupancy level")
	}
	if reqs[1].ParentToolUseID != "toolu_01Sub" {
		t.Errorf("parent tool use id = %q, want toolu_01Sub", reqs[1].ParentToolUseID)
	}
	// The usage itself is still carried: a subagent's prompt is billed.
	if reqs[1].Occupancy() != 2100 {
		t.Errorf("subagent occupancy = %d, want 2100", reqs[1].Occupancy())
	}
}

func TestTurnDataRequestOmittedFromJSONWhenAbsent(t *testing.T) {
	t.Parallel()
	// Request is additive under schema_version 1, so a turn event without
	// it must serialize exactly as it did before the field existed.
	b, err := json.Marshal(events.TurnData{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(b, []byte("request")) {
		t.Errorf("request leaked into JSON: %s", b)
	}
}
