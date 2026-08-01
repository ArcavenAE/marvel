package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/runtime/events"
)

func fixedClock() func() time.Time {
	t := time.Date(2026, 7, 31, 20, 12, 31, 0, time.UTC)
	return func() time.Time {
		t = t.Add(time.Millisecond)
		return t
	}
}

func collectFixture(t *testing.T, name string) []events.Event {
	t.Helper()
	path := filepath.Join("testdata", name)
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { _ = f.Close() })

	p := NewParser(Config{
		AgentID:   "worker-c",
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

func kinds(evs []events.Event) []events.Kind {
	out := make([]events.Kind, len(evs))
	for i, e := range evs {
		out[i] = e.Event
	}
	return out
}

func TestParseHelloFixture(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "hello.jsonl")

	// hello.jsonl (4 vendor lines):
	//   thread.started   → session.started
	//   turn.started     → turn.started
	//   item.completed(agent_message) → message.completed
	//   turn.completed   → turn.completed
	wantKinds := []events.Kind{
		events.KindSessionStarted,
		events.KindTurnStarted,
		events.KindMessageCompleted,
		events.KindTurnCompleted,
	}
	if len(got) != len(wantKinds) {
		t.Fatalf("event count = %d, want %d — kinds: %v", len(got), len(wantKinds), kinds(got))
	}
	for i, ev := range got {
		if ev.Event != wantKinds[i] {
			t.Errorf("event[%d].kind = %s, want %s", i, ev.Event, wantKinds[i])
		}
	}

	msg, ok := got[2].Data.(events.MessageData)
	if !ok {
		t.Fatalf("event[2].Data type = %T, want MessageData", got[2].Data)
	}
	if msg.Role != "assistant" {
		t.Errorf("message role = %q, want assistant", msg.Role)
	}
	if msg.Text != "ok" {
		t.Errorf("message text = %q, want ok", msg.Text)
	}

	turn, ok := got[3].Data.(events.TurnData)
	if !ok {
		t.Fatalf("event[3].Data type = %T, want TurnData", got[3].Data)
	}
	// usage: input_tokens 13992, output_tokens 5.
	if turn.UsageDelta.In != 13992 {
		t.Errorf("turn usage.in = %d, want 13992", turn.UsageDelta.In)
	}
	if turn.UsageDelta.Out != 5 {
		t.Errorf("turn usage.out = %d, want 5", turn.UsageDelta.Out)
	}
	// Codex reports no cost.
	if turn.UsageDelta.Cost != nil {
		t.Errorf("turn usage.cost = %v, want nil (codex reports no cost)", turn.UsageDelta.Cost)
	}
}

func TestParseThreadIDPropagation(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "hello.jsonl")
	const want = "019fba85-383c-7300-a578-d3b7c4c6f607"
	for i, ev := range got {
		if ev.HarnessSessionID != want {
			t.Errorf("event[%d] harness_session_id = %q, want %q", i, ev.HarnessSessionID, want)
		}
	}
}

func TestParseStampsIdentity(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "hello.jsonl")
	for i, ev := range got {
		if ev.AgentID != "worker-c" {
			t.Errorf("event[%d].agent_id = %q, want worker-c", i, ev.AgentID)
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
	got := collectFixture(t, "hello.jsonl")
	for i, ev := range got {
		if ev.Seq != uint64(i+1) {
			t.Errorf("event[%d].seq = %d, want %d", i, ev.Seq, i+1)
		}
	}
}

func TestParseToolCallFixture(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "tool_call.jsonl")

	// tool_call.jsonl (7 vendor lines):
	//   thread.started                        → session.started
	//   turn.started                          → turn.started
	//   item.completed(agent_message)         → message.completed
	//   item.started(command_execution)       → tool.call
	//   item.completed(command_execution)     → tool.result
	//   item.completed(agent_message)         → message.completed
	//   turn.completed                        → turn.completed
	wantKinds := []events.Kind{
		events.KindSessionStarted,
		events.KindTurnStarted,
		events.KindMessageCompleted,
		events.KindToolCall,
		events.KindToolResult,
		events.KindMessageCompleted,
		events.KindTurnCompleted,
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

func TestParseToolCallCorrelationAndResult(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "tool_call.jsonl")

	var call events.ToolCallData
	var result events.ToolResultData
	var haveCall, haveResult bool
	for _, ev := range got {
		switch ev.Event {
		case events.KindToolCall:
			call = ev.Data.(events.ToolCallData)
			haveCall = true
		case events.KindToolResult:
			result = ev.Data.(events.ToolResultData)
			haveResult = true
		}
	}
	if !haveCall || !haveResult {
		t.Fatalf("missing tool events: call=%v result=%v", haveCall, haveResult)
	}
	if call.CallID == "" || call.CallID != result.CallID {
		t.Errorf("call/result correlation: call=%q result=%q", call.CallID, result.CallID)
	}
	if call.Tool != "command_execution" {
		t.Errorf("tool = %q, want command_execution", call.Tool)
	}
	if in, ok := call.Input.(string); !ok || !strings.Contains(in, "echo hello-from-codex") {
		t.Errorf("tool input = %v, want the command string", call.Input)
	}
	// exit_code 0 + status completed → ok.
	if !result.OK {
		t.Error("tool.result should be ok (exit_code 0, status completed)")
	}
	if out, ok := result.Output.(string); !ok || !strings.Contains(out, "hello-from-codex") {
		t.Errorf("tool output = %v, want the aggregated output", result.Output)
	}
}

func TestCommandOK(t *testing.T) {
	t.Parallel()
	zero := 0
	one := 1
	tests := []struct {
		status string
		exit   *int
		want   bool
	}{
		{"completed", &zero, true},
		{"completed", &one, false},
		{"completed", nil, false},
		{"in_progress", nil, false},
		{"failed", &zero, false},
	}
	for _, tt := range tests {
		if got := commandOK(tt.status, tt.exit); got != tt.want {
			t.Errorf("commandOK(%q, %v) = %v, want %v", tt.status, tt.exit, got, tt.want)
		}
	}
}

func TestParseReasoningIsUnmapped(t *testing.T) {
	t.Parallel()
	line := `{"type":"item.completed","item":{"id":"item_9","type":"reasoning","text":"pondering"}}`
	got := parseLines(t, line)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if got[0].Event != events.KindError {
		t.Fatalf("kind = %s, want error", got[0].Event)
	}
	d := got[0].Data.(events.ErrorData)
	if d.Kind != events.ErrKindUnmapped {
		t.Errorf("error kind = %q, want unmapped", d.Kind)
	}
	if len(got[0].Raw) == 0 {
		t.Error("unmapped reasoning must preserve raw")
	}
}

func TestParseUnknownItemTypePreservesRaw(t *testing.T) {
	t.Parallel()
	line := `{"type":"item.completed","item":{"id":"item_9","type":"mcp_tool_call"}}`
	got := parseLines(t, line)
	if len(got) != 1 || got[0].Event != events.KindError {
		t.Fatalf("kinds = %v, want one error", kinds(got))
	}
	d := got[0].Data.(events.ErrorData)
	if d.Kind != events.ErrKindUnmapped {
		t.Errorf("error kind = %q, want unmapped", d.Kind)
	}
	var check any
	if err := json.Unmarshal(got[0].Raw, &check); err != nil {
		t.Errorf("raw not valid JSON: %v", err)
	}
}

func TestParseUnknownTopLevelType(t *testing.T) {
	t.Parallel()
	got := parseLines(t, `{"type":"turn.aborted","reason":"x"}`)
	if len(got) != 1 || got[0].Event != events.KindError {
		t.Fatalf("kinds = %v, want one error", kinds(got))
	}
	if got[0].Data.(events.ErrorData).Kind != events.ErrKindUnmapped {
		t.Errorf("kind = %q, want unmapped", got[0].Data.(events.ErrorData).Kind)
	}
}

func TestParseMalformedLineEmitsParseError(t *testing.T) {
	t.Parallel()
	stream := strings.Join([]string{
		`{"type":"thread.started","thread_id":"t1"}`,
		`{not json}`,
		`{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":2}}`,
	}, "\n")
	got := parseLines(t, stream)
	if len(got) != 3 {
		t.Fatalf("event count = %d, want 3: %v", len(got), kinds(got))
	}
	if got[1].Event != events.KindError || got[1].Data.(events.ErrorData).Kind != events.ErrKindParse {
		t.Errorf("got[1] = %s/%+v, want error/parse", got[1].Event, got[1].Data)
	}
}

func TestParseEmptyLinesSkipped(t *testing.T) {
	t.Parallel()
	got := parseLines(t, "\n\n"+`{"type":"turn.started"}`+"\n\n")
	if len(got) != 1 {
		t.Fatalf("event count = %d, want 1 (blanks skipped)", len(got))
	}
}

// parseLines runs the parser over a literal stream with a fixed clock.
func parseLines(t *testing.T, stream string) []events.Event {
	t.Helper()
	p := NewParser(Config{Clock: fixedClock()})
	var got []events.Event
	if err := p.Parse(context.Background(), strings.NewReader(stream), func(e events.Event) {
		got = append(got, e)
	}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return got
}

func TestParseTurnUsagePromotesCacheClasses(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "hello.jsonl")

	turn, ok := got[3].Data.(events.TurnData)
	if !ok {
		t.Fatalf("event[3].Data type = %T, want TurnData", got[3].Data)
	}
	r := turn.Request
	if r == nil {
		t.Fatal("turn.completed carries no Request")
	}
	// usage: input_tokens 13992 with cached_input_tokens 11008 inside it.
	if r.In != 13992 {
		t.Errorf("request in = %d, want 13992", r.In)
	}
	if r.CacheReadIn != 11008 {
		t.Errorf("request cache_read_in = %d, want 11008", r.CacheReadIn)
	}
	if r.Layout != events.LayoutSubsumptive {
		t.Errorf("layout = %q, want subsumptive", r.Layout)
	}
	// The point of the subsumptive layout: cached tokens are already in
	// In, so occupancy is In alone and never the 25000 a sum would give.
	if got := r.Occupancy(); got != 13992 {
		t.Errorf("occupancy = %d, want 13992 (In alone, not In+cache)", got)
	}
	// No total_tokens on the wire, so the layout invariant is disabled.
	if r.Total != 0 {
		t.Errorf("total = %d, want 0", r.Total)
	}
	if r.TotalMismatch() != 0 {
		t.Errorf("total mismatch = %d, want 0 when no total is published", r.TotalMismatch())
	}
	if r.Cost != nil {
		t.Errorf("cost = %v, want nil (codex reports no cost)", r.Cost)
	}
}

// TestParseResumeOccupancyIsALevel exercises the multi-turn occupancy
// series codex `exec` one-shot cannot produce.
//
// SYNTHETIC FIXTURE: resume.jsonl is hand-composed from two real
// turn.completed payloads. Whether codex's input_tokens is a per-turn
// level or a running session total is UNVERIFIED against a real capture;
// `codex exec resume` is the command that settles it. This test pins the
// level reading the parser and accountant assume.
func TestParseResumeOccupancyIsALevel(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "resume.jsonl")

	var occ []int
	for _, ev := range got {
		if ev.Event != events.KindTurnCompleted {
			continue
		}
		r := ev.Data.(events.TurnData).Request
		if r == nil {
			t.Fatal("turn.completed carries no Request")
		}
		occ = append(occ, r.Occupancy())
	}
	want := []int{13992, 28110}
	if len(occ) != len(want) {
		t.Fatalf("got %d turn occupancies, want %d: %v", len(occ), len(want), occ)
	}
	for i := range want {
		if occ[i] != want[i] {
			t.Errorf("turn[%d] occupancy = %d, want %d", i, occ[i], want[i])
		}
	}
}
