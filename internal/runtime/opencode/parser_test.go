package opencode

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
		AgentID:   "worker-o",
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

	// hello.jsonl (3 vendor lines):
	//   step_start  → turn.started
	//   text        → message.completed
	//   step_finish → turn.completed
	wantKinds := []events.Kind{
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

	msg := got[1].Data.(events.MessageData)
	if msg.Role != "assistant" || msg.Text != "ok" {
		t.Errorf("message = %+v, want assistant/ok", msg)
	}

	turn := got[2].Data.(events.TurnData)
	// step_finish tokens: input 29878, output 2, cost 0.
	if turn.UsageDelta.In != 29878 {
		t.Errorf("usage.in = %d, want 29878", turn.UsageDelta.In)
	}
	if turn.UsageDelta.Out != 2 {
		t.Errorf("usage.out = %d, want 2", turn.UsageDelta.Out)
	}
	// OpenCode reports cost (0 here); it must be present, not nil.
	if turn.UsageDelta.Cost == nil {
		t.Fatal("usage.cost is nil; opencode reports cost")
	}
	if *turn.UsageDelta.Cost != 0 {
		t.Errorf("usage.cost = %v, want 0", *turn.UsageDelta.Cost)
	}
}

func TestParseSessionIDPropagation(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "hello.jsonl")
	const want = "ses_04575aed0ffejX7cCQyeoswmpL"
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
		if ev.AgentID != "worker-o" {
			t.Errorf("event[%d].agent_id = %q, want worker-o", i, ev.AgentID)
		}
		if ev.Workspace != "aae-orc" {
			t.Errorf("event[%d].workspace = %q, want aae-orc", i, ev.Workspace)
		}
		if ev.Harness != Harness {
			t.Errorf("event[%d].harness = %q, want %q", i, ev.Harness, Harness)
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

func TestParseToolFixture(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "tool_call.jsonl")

	// tool_call.jsonl (3 vendor lines):
	//   step_start           → turn.started
	//   tool_use(state error) → tool.call + tool.result(ok=false)
	//   step_finish          → turn.completed
	wantKinds := []events.Kind{
		events.KindTurnStarted,
		events.KindToolCall,
		events.KindToolResult,
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

	call := got[1].Data.(events.ToolCallData)
	result := got[2].Data.(events.ToolResultData)
	if call.Tool != "bash" {
		t.Errorf("tool = %q, want bash", call.Tool)
	}
	if call.CallID == "" || call.CallID != result.CallID {
		t.Errorf("correlation: call=%q result=%q", call.CallID, result.CallID)
	}
	// The fixture's tool state is a permission rejection (status error).
	if result.OK {
		t.Error("rejected tool.result should be ok=false")
	}
	if out, ok := result.Output.(string); !ok || !strings.Contains(out, "rejected permission") {
		t.Errorf("tool output = %v, want the rejection text", result.Output)
	}
}

func TestParseErrorFixture(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "error.jsonl")
	if len(got) != 1 {
		t.Fatalf("event count = %d, want 1", len(got))
	}
	if got[0].Event != events.KindError {
		t.Fatalf("kind = %s, want error", got[0].Event)
	}
	d := got[0].Data.(events.ErrorData)
	if d.Kind != "APIError" {
		t.Errorf("error kind = %q, want APIError", d.Kind)
	}
	if !strings.Contains(d.Message, "qwen3-coder") {
		t.Errorf("error message = %q, want the vendor message", d.Message)
	}
	if got[0].HarnessSessionID == "" {
		t.Error("error event lost the session id")
	}
}

func TestParseReasoningIsUnmapped(t *testing.T) {
	t.Parallel()
	line := `{"type":"reasoning","sessionID":"ses_1","part":{"type":"reasoning","text":"pondering"}}`
	got := parseLines(t, line)
	if len(got) != 1 || got[0].Event != events.KindError {
		t.Fatalf("kinds = %v, want one error", kinds(got))
	}
	if got[0].Data.(events.ErrorData).Kind != events.ErrKindUnmapped {
		t.Errorf("kind = %q, want unmapped", got[0].Data.(events.ErrorData).Kind)
	}
	if len(got[0].Raw) == 0 {
		t.Error("unmapped reasoning must preserve raw")
	}
}

func TestParseCompletedToolIsOK(t *testing.T) {
	t.Parallel()
	line := `{"type":"tool_use","sessionID":"ses_1","part":{"type":"tool","tool":"read","callID":"c1","state":{"status":"completed","input":{"path":"/etc/hosts"},"output":"127.0.0.1 localhost"}}}`
	got := parseLines(t, line)
	if len(got) != 2 {
		t.Fatalf("event count = %d, want 2 (call + result)", len(got))
	}
	result := got[1].Data.(events.ToolResultData)
	if !result.OK {
		t.Error("completed tool.result should be ok=true")
	}
	if out, ok := result.Output.(string); !ok || out != "127.0.0.1 localhost" {
		t.Errorf("output = %v, want the tool output string", result.Output)
	}
}

func TestParseRunningToolYieldsOnlyCall(t *testing.T) {
	t.Parallel()
	// A non-terminal state (streaming) yields the call but no result yet.
	line := `{"type":"tool_use","sessionID":"ses_1","part":{"type":"tool","tool":"bash","callID":"c1","state":{"status":"running","input":{"command":"sleep 1"}}}}`
	got := parseLines(t, line)
	if len(got) != 1 || got[0].Event != events.KindToolCall {
		t.Fatalf("kinds = %v, want one tool.call", kinds(got))
	}
}

func TestParseUnknownTopLevelType(t *testing.T) {
	t.Parallel()
	got := parseLines(t, `{"type":"session_idle","sessionID":"ses_1"}`)
	if len(got) != 1 || got[0].Event != events.KindError {
		t.Fatalf("kinds = %v, want one error", kinds(got))
	}
	if got[0].Data.(events.ErrorData).Kind != events.ErrKindUnmapped {
		t.Errorf("kind = %q, want unmapped", got[0].Data.(events.ErrorData).Kind)
	}
	// session id was learned before dispatch.
	if got[0].HarnessSessionID != "ses_1" {
		t.Errorf("session id = %q, want ses_1", got[0].HarnessSessionID)
	}
}

func TestParseMalformedLineEmitsParseError(t *testing.T) {
	t.Parallel()
	stream := strings.Join([]string{
		`{"type":"step_start","sessionID":"ses_1","part":{"type":"step-start"}}`,
		`{not json}`,
		`{"type":"step_finish","sessionID":"ses_1","part":{"type":"step-finish","tokens":{"input":1,"output":2},"cost":0}}`,
	}, "\n")
	got := parseLines(t, stream)
	if len(got) != 3 {
		t.Fatalf("event count = %d, want 3: %v", len(got), kinds(got))
	}
	if got[1].Event != events.KindError || got[1].Data.(events.ErrorData).Kind != events.ErrKindParse {
		t.Errorf("got[1] = %s/%+v, want error/parse", got[1].Event, got[1].Data)
	}
}

func TestSessionIDPreservedAcrossToken(t *testing.T) {
	t.Parallel()
	got := parseLines(t, `{"type":"tool_use","sessionID":"ses_x","part":{"type":"tool","tool":"bash","callID":"c1","state":{"status":"error","error":"nope"}}}`)
	for i, ev := range got {
		if ev.HarnessSessionID != "ses_x" {
			t.Errorf("event[%d] session id = %q, want ses_x", i, ev.HarnessSessionID)
		}
	}
}

func TestRawIsValidJSONOnUnmapped(t *testing.T) {
	t.Parallel()
	got := parseLines(t, `{"type":"totally_new","sessionID":"ses_1","foo":"bar"}`)
	var check any
	if err := json.Unmarshal(got[0].Raw, &check); err != nil {
		t.Errorf("raw not valid JSON: %v", err)
	}
}

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

func TestParseStepFinishPromotesEveryTokenClass(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "hello.jsonl")

	var r *events.RequestUsage
	for _, ev := range got {
		if ev.Event == events.KindTurnCompleted {
			r = ev.Data.(events.TurnData).Request
		}
	}
	if r == nil {
		t.Fatal("turn.completed carries no Request")
	}
	// tokens: {total 29907, input 29878, output 2, reasoning 27,
	//          cache {write 0, read 0}}
	if r.In != 29878 || r.Out != 2 || r.ReasoningOut != 27 || r.Total != 29907 {
		t.Errorf("request = in %d out %d reasoning %d total %d, want 29878/2/27/29907",
			r.In, r.Out, r.ReasoningOut, r.Total)
	}
	if r.CacheReadIn != 0 || r.CacheCreationIn != 0 {
		t.Errorf("cache = read %d write %d, want 0/0 for the free model", r.CacheReadIn, r.CacheCreationIn)
	}
	if r.Layout != events.LayoutAdditive {
		t.Errorf("layout = %q, want additive", r.Layout)
	}
	if r.Cost == nil {
		t.Fatal("cost unset; opencode reports one (0 for the free model)")
	}
	// The total covers input + output + reasoning (measured), which is
	// what TotalExcludesCache declares, so the invariant is silent on a
	// well-formed line.
	if !r.TotalExcludesCache {
		t.Error("total_excludes_cache unset; the comparison would then include classes opencode's total omits")
	}
	if r.TotalMismatch() != 0 {
		t.Errorf("total mismatch = %d, want 0 on a well-formed line", r.TotalMismatch())
	}
	// Nothing to confirm without a cache read.
	if r.AdditiveConfirmed() {
		t.Error("a non-caching turn cannot confirm the cache layout")
	}
}

// TestParseCachingTurnConfirmsAdditiveLayout covers the one unverified
// premise in this adapter, whether tokens.input already subsumes
// cache.read, and the one signal that speaks to it.
//
// SYNTHETIC FIXTURE. caching.jsonl is a warm caching turn in the shape the
// additive reading implies: a small input against a large cache read, with
// total == input + output + reasoning as measured on the free model
// (finding-007). Under that shape input cannot contain cache.read, which
// is what AdditiveConfirmed reports. The check is one-sided; see the
// subsumptive case below.
func TestParseCachingTurnConfirmsAdditiveLayout(t *testing.T) {
	t.Parallel()
	got := collectFixture(t, "caching.jsonl")

	var r *events.RequestUsage
	for _, ev := range got {
		if ev.Event == events.KindTurnCompleted {
			r = ev.Data.(events.TurnData).Request
		}
	}
	if r == nil {
		t.Fatal("turn.completed carries no Request")
	}
	if r.CacheReadIn == 0 && r.CacheCreationIn == 0 {
		t.Fatal("fixture no longer exercises a caching model")
	}
	// The measured total definition must not read as a layout fault: it is
	// the same number under either reading of input, so a mismatch here
	// would fire on every caching turn and mean nothing.
	if got := r.TotalMismatch(); got != 0 {
		t.Errorf("total mismatch = %d, want 0; opencode's total omits the cache classes by definition", got)
	}
	if !r.AdditiveConfirmed() {
		t.Errorf("input %d against cache read %d must confirm the additive reading", r.In, r.CacheReadIn)
	}
	if got := r.Occupancy(); got != 29240 {
		t.Errorf("occupancy = %d, want 29240 (input + cache read + cache write)", got)
	}
}

// TestParseSubsumptiveShapeCannotBeConfirmed is the other half, and the
// reason AdditiveConfirmed is documented as one-sided: if tokens.input
// DOES contain the cached tokens it is necessarily the larger number, so
// no line in that world can be distinguished from a cold additive one.
// Marvel would over-count occupancy by the cache classes and nothing in
// the stream would say so. One paid caching turn is the only settlement.
func TestParseSubsumptiveShapeCannotBeConfirmed(t *testing.T) {
	t.Parallel()
	got := parseLines(t, `{"type":"step_finish","sessionID":"ses_1","part":{"type":"step-finish","tokens":{"total":29257,"input":29240,"output":12,"reasoning":5,"cache":{"write":200,"read":29000}},"cost":0}}`)
	r := got[0].Data.(events.TurnData).Request
	if r == nil {
		t.Fatal("turn.completed carries no Request")
	}
	if r.TotalMismatch() != 0 {
		t.Errorf("total mismatch = %d, want 0: the total is well-formed under either reading", r.TotalMismatch())
	}
	if r.AdditiveConfirmed() {
		t.Error("a subsumptive input must not confirm the additive reading")
	}
}

// A total that stops matching input + output + reasoning is a vendor
// schema change, and that is the one thing the total is asked to catch.
func TestParseTotalDefinitionChangeIsReported(t *testing.T) {
	t.Parallel()
	got := parseLines(t, `{"type":"step_finish","sessionID":"ses_1","part":{"type":"step-finish","tokens":{"total":29257,"input":40,"output":12,"reasoning":5,"cache":{"write":200,"read":29000}},"cost":0}}`)
	r := got[0].Data.(events.TurnData).Request
	if r == nil {
		t.Fatal("turn.completed carries no Request")
	}
	if got := r.TotalMismatch(); got != 29200 {
		t.Errorf("total mismatch = %d, want 29200 (29257 - (40+12+5))", got)
	}
}
