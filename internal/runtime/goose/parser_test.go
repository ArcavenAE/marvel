package goose

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/runtime/events"
)

// fixtureDir holds the recorded goose v1.57.0 runs (goose-arm2/README.md). The
// parser is tested against them, not against shapes written from memory.
const fixtureDir = "../testdata/goose-arm2"

func fixedClock() func() time.Time {
	t := time.Date(2026, 11, 3, 4, 0, 0, 0, time.UTC)
	return func() time.Time {
		t = t.Add(time.Millisecond)
		return t
	}
}

func parseString(t *testing.T, stream string) []events.Event {
	t.Helper()
	p := NewParser(Config{AgentID: "worker-g", Workspace: "aae", Clock: fixedClock()})
	var out []events.Event
	if err := p.Parse(context.Background(), strings.NewReader(stream), func(e events.Event) {
		out = append(out, e)
	}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return out
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtureDir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func parseFixture(t *testing.T, name string) []events.Event {
	t.Helper()
	return parseString(t, readFixture(t, name))
}

func kinds(evs []events.Event) []events.Kind {
	out := make([]events.Kind, len(evs))
	for i, e := range evs {
		out[i] = e.Event
	}
	return out
}

func wantKinds(t *testing.T, got []events.Event, want ...events.Kind) {
	t.Helper()
	g := kinds(got)
	if len(g) != len(want) {
		t.Fatalf("kinds = %v, want %v", g, want)
	}
	for i := range g {
		if g[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", g, want)
		}
	}
}

func ended(t *testing.T, e events.Event) events.SessionEndedData {
	t.Helper()
	d, ok := e.Data.(events.SessionEndedData)
	if !ok {
		t.Fatalf("event %s carries %T, want SessionEndedData", e.Event, e.Data)
	}
	return d
}

func TestParseR1ToolCallAndAnswer(t *testing.T) {
	t.Parallel()
	got := parseFixture(t, "R1.stdout.jsonl")
	// thinking -> none; toolRequest -> tool.call; toolResponse -> tool.result;
	// text -> message.delta; complete -> session.ended.
	wantKinds(t, got, events.KindToolCall, events.KindToolResult, events.KindMessageDelta, events.KindSessionEnded)

	call := got[0].Data.(events.ToolCallData)
	if call.Tool != "tree" || call.CallID != "call_71iagys3" {
		t.Errorf("tool.call = %+v", call)
	}
	res := got[1].Data.(events.ToolResultData)
	if res.CallID != "call_71iagys3" || !res.OK {
		t.Errorf("tool.result = %+v, want ok for call_71iagys3", res)
	}
	if out, _ := res.Output.(string); !strings.Contains(out, "one.txt") {
		t.Errorf("tool.result output = %v, want the tree text", res.Output)
	}
	if d := got[2].Data.(events.MessageData); d.Role != "assistant" || d.Text != "There" {
		t.Errorf("message.delta = %+v", d)
	}
	end := ended(t, got[3])
	if end.ExitCode != 0 || end.Usage.In != 6485 || end.Usage.Out != 354 || end.Usage.Cost != nil {
		t.Errorf("session.ended = %+v", end)
	}
	if end.Metering == nil || end.Metering.CacheReadIn != 3217 || end.Metering.CacheCreationIn != 0 || len(end.Metering.PermissionDenials) != 0 {
		t.Errorf("metering = %+v", end.Metering)
	}
	for _, e := range got {
		if e.Harness != Harness || e.AgentID != "worker-g" || e.Workspace != "aae" {
			t.Errorf("event %s identity = %q %q %q", e.Event, e.Harness, e.AgentID, e.Workspace)
		}
	}
}

// R3 is the C4 case: shell was declined, goose exited 0, and the model said the
// command ran. Only the tool result says what ran.
func TestParseR3DeniedToolIsNotRun(t *testing.T) {
	t.Parallel()
	got := parseFixture(t, "R3.stdout.jsonl")
	wantKinds(t, got, events.KindToolCall, events.KindToolResult, events.KindMessageDelta, events.KindSessionEnded)

	res := got[1].Data.(events.ToolResultData)
	if res.CallID != "call_ngnvd7s5" || res.OK {
		t.Errorf("tool.result = %+v, want not ok for call_ngnvd7s5", res)
	}
	end := ended(t, got[3])
	if end.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0 (complete)", end.ExitCode)
	}
	if end.Metering == nil || len(end.Metering.PermissionDenials) != 1 {
		t.Fatalf("metering = %+v, want one denial", end.Metering)
	}
	if d := end.Metering.PermissionDenials[0]; d.Tool != "shell" || d.CallID != "call_ngnvd7s5" {
		t.Errorf("denial = %+v, want shell/call_ngnvd7s5", d)
	}
	for _, e := range got {
		if e.Event == events.KindToolResult && e.Data.(events.ToolResultData).OK {
			t.Errorf("an event says the declined command ran: %+v", e)
		}
	}
}

// The same shape with isError false counts one run and no denial.
func TestParseR3ShapeWithIsErrorFalseCountsOneRun(t *testing.T) {
	t.Parallel()
	stream := readFixture(t, "R3.stdout.jsonl")
	if !strings.Contains(stream, `"isError":true`) {
		t.Fatal("R3 no longer carries isError true")
	}
	got := parseString(t, strings.Replace(stream, `"isError":true`, `"isError":false`, 1))
	res := got[1].Data.(events.ToolResultData)
	if !res.OK {
		t.Errorf("tool.result = %+v, want ok", res)
	}
	if end := ended(t, got[len(got)-1]); end.Metering != nil && len(end.Metering.PermissionDenials) != 0 {
		t.Errorf("denials = %+v, want none", end.Metering.PermissionDenials)
	}
}

// A response whose status is not "success" did not run, whatever isError says.
func TestParseToolResponseStatusAloneIsNotTrusted(t *testing.T) {
	t.Parallel()
	stream := `{"type":"message","message":{"id":"m1","role":"assistant","created":1,"content":[{"type":"toolRequest","id":"c1","toolCall":{"status":"success","value":{"name":"write","arguments":{}}}}],"metadata":{}}}
{"type":"message","message":{"id":"m2","role":"user","created":1,"content":[{"type":"toolResponse","id":"c1","toolResult":{"status":"error","value":"boom"}}],"metadata":{}}}
{"type":"complete","total_tokens":1,"input_tokens":1,"output_tokens":0,"cache_read_input_tokens":0,"cache_write_input_tokens":0}
`
	got := parseString(t, stream)
	wantKinds(t, got, events.KindToolCall, events.KindToolResult, events.KindSessionEnded)
	if got[1].Data.(events.ToolResultData).OK {
		t.Error("a status:error response counted as run")
	}
	end := ended(t, got[2])
	if end.Metering == nil || len(end.Metering.PermissionDenials) != 1 || end.Metering.PermissionDenials[0].Tool != "write" {
		t.Errorf("metering = %+v, want one not-ran entry for write", end.Metering)
	}
}

// R2: a tool outside the permission list ends the run with no complete line.
// The parser reports the call and nothing else; the exit code, the missing
// complete and the stderr tail decide it at the reap.
func TestParseR2HasNoSessionEnded(t *testing.T) {
	t.Parallel()
	got := parseFixture(t, "R2.stdout.jsonl")
	wantKinds(t, got, events.KindToolCall)
	if c := got[0].Data.(events.ToolCallData); c.Tool != "write" || c.CallID != "call_qov5ajg3" {
		t.Errorf("tool.call = %+v", c)
	}
}

// A request with no response by complete is not-ran: the call stays, there is no
// result, and it is not listed.
func TestParseRequestWithoutResponseIsNotListed(t *testing.T) {
	t.Parallel()
	stream := `{"type":"message","message":{"id":"m1","role":"assistant","created":1,"content":[{"type":"toolRequest","id":"c1","toolCall":{"status":"success","value":{"name":"shell","arguments":{"command":"ls"}}}}],"metadata":{}}}
{"type":"complete","total_tokens":1,"input_tokens":1,"output_tokens":0,"cache_read_input_tokens":0,"cache_write_input_tokens":0}
`
	got := parseString(t, stream)
	wantKinds(t, got, events.KindToolCall, events.KindSessionEnded)
	if end := ended(t, got[1]); end.Metering != nil {
		t.Errorf("metering = %+v, want nil", end.Metering)
	}
}

// R4: a failed request. The content error item and the error line carry the
// same text; one error event results.
func TestParseR4ErrorLineEmitsOneError(t *testing.T) {
	t.Parallel()
	got := parseFixture(t, "R4.stdout.jsonl")
	wantKinds(t, got, events.KindError)
	d := got[0].Data.(events.ErrorData)
	if d.Kind != ErrKindVendor || !strings.Contains(d.Message, "Resource not found (404)") {
		t.Errorf("error = %+v", d)
	}
}

func TestParseR5PlainAnswer(t *testing.T) {
	t.Parallel()
	got := parseFixture(t, "R5.stdout.jsonl")
	wantKinds(t, got, events.KindMessageDelta, events.KindSessionEnded)
	if d := got[0].Data.(events.MessageData); d.Text != "ok" {
		t.Errorf("message.delta = %+v", d)
	}
	end := ended(t, got[1])
	if end.Usage.In != 3201 || end.Usage.Out != 144 {
		t.Errorf("usage = %+v", end.Usage)
	}
	if end.Metering == nil || end.Metering.CacheReadIn != 3 {
		t.Errorf("metering = %+v", end.Metering)
	}
}

// R9 is the one multi-tool sequence: nine parallel calls on one line, nine
// responses on the next. They match by id.
func TestParseR9ParallelCallsMatchByID(t *testing.T) {
	t.Parallel()
	got := parseFixture(t, "R9.stdout.jsonl")
	calls, results := map[string]bool{}, map[string]bool{}
	var order []events.Kind
	for _, e := range got {
		order = append(order, e.Event)
		switch d := e.Data.(type) {
		case events.ToolCallData:
			if d.Tool != "tree" {
				t.Errorf("call %s tool = %q", d.CallID, d.Tool)
			}
			calls[d.CallID] = true
		case events.ToolResultData:
			if !d.OK {
				t.Errorf("result %s not ok", d.CallID)
			}
			results[d.CallID] = true
		}
	}
	if len(calls) != 9 || len(results) != 9 {
		t.Fatalf("%d calls and %d results, want 9 and 9 (%v)", len(calls), len(results), order)
	}
	for id := range calls {
		if !results[id] {
			t.Errorf("call %s has no result", id)
		}
	}
	if last := got[len(got)-1]; last.Event != events.KindSessionEnded || ended(t, last).Usage.Out != 1206 {
		t.Errorf("last event = %+v", last)
	}
}

// Responses that arrive in another order than their requests are still matched
// by id: the denial names the tool of its own request.
func TestParseDenialNamesToolByIDNotPosition(t *testing.T) {
	t.Parallel()
	stream := `{"type":"message","message":{"id":"m1","role":"assistant","created":1,"content":[{"type":"toolRequest","id":"a","toolCall":{"status":"success","value":{"name":"tree","arguments":{}}}},{"type":"toolRequest","id":"b","toolCall":{"status":"success","value":{"name":"shell","arguments":{}}}}],"metadata":{}}}
{"type":"message","message":{"id":"m2","role":"user","created":1,"content":[{"type":"toolResponse","id":"b","toolResult":{"status":"success","value":{"resultType":"complete","content":[{"type":"text","text":"declined"}],"isError":true}}},{"type":"toolResponse","id":"a","toolResult":{"status":"success","value":{"resultType":"complete","content":[{"type":"text","text":"fine"}],"isError":false}}}],"metadata":{}}}
{"type":"complete","total_tokens":1,"input_tokens":1,"output_tokens":0,"cache_read_input_tokens":0,"cache_write_input_tokens":0}
`
	got := parseString(t, stream)
	end := ended(t, got[len(got)-1])
	if end.Metering == nil || len(end.Metering.PermissionDenials) != 1 {
		t.Fatalf("metering = %+v", end.Metering)
	}
	if d := end.Metering.PermissionDenials[0]; d.Tool != "shell" || d.CallID != "b" {
		t.Errorf("denial = %+v, want shell/b", d)
	}
	for _, e := range got {
		if r, ok := e.Data.(events.ToolResultData); ok && r.OK != (r.CallID == "a") {
			t.Errorf("result %s ok=%v", r.CallID, r.OK)
		}
	}
}

func TestParseR10TextTailAfterThinking(t *testing.T) {
	t.Parallel()
	got := parseFixture(t, "R10.stdout.jsonl")
	wantKinds(t, got, events.KindMessageDelta, events.KindMessageDelta, events.KindSessionEnded)
	if d := got[0].Data.(events.MessageData); d.Text != "!" {
		t.Errorf("first delta = %+v", d)
	}
	if end := ended(t, got[2]); end.Usage.Out != 15976 {
		t.Errorf("usage = %+v", end.Usage)
	}
}

// R11 ends at the output token limit: an assistant message with empty content
// carries the flag, and complete still follows. The flag stays visible.
func TestParseR11OutputLimitIsReported(t *testing.T) {
	t.Parallel()
	got := parseFixture(t, "R11.stdout.jsonl")
	wantKinds(t, got, events.KindMessageDelta, events.KindError, events.KindSessionEnded)
	d := got[1].Data.(events.ErrorData)
	if d.Kind != ErrKindOutputLimit || d.Message != "goose reported outputTokenLimitReached" {
		t.Errorf("error = %+v", d)
	}
	if end := ended(t, got[2]); end.ExitCode != 0 || end.Usage.Out != 40960 {
		t.Errorf("session.ended = %+v", end)
	}
}

func TestParseOutputLimitFlagReadWhateverTheContentHolds(t *testing.T) {
	t.Parallel()
	line := func(content string) string {
		return `{"type":"message","message":{"id":"m","role":"assistant","created":1,"content":` + content + `,"metadata":{"outputTokenLimitReached":true}}}` + "\n"
	}
	stream := line(`[{"type":"text","text":"cut"}]`) + line(`[]`) + line(`[]`)
	got := parseString(t, stream)
	wantKinds(t, got, events.KindMessageDelta, events.KindError)
}

func TestParseEmptyContentMessageIsANoOp(t *testing.T) {
	t.Parallel()
	got := parseString(t, `{"type":"message","message":{"id":"m","role":"assistant","created":1,"content":[],"metadata":{}}}`+"\n")
	if len(got) != 0 {
		t.Fatalf("events = %v, want none", kinds(got))
	}
}

func TestParseUnknownShapes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		line     string
		wantKind string
		wantMsg  string
	}{
		{"notification line", `{"type":"notification","extension":"x","message":"y"}`, events.ErrKindUnmapped, "notification"},
		{"unknown line type", `{"type":"surprise"}`, events.ErrKindUnmapped, "surprise"},
		{"unknown content item", `{"type":"message","message":{"role":"assistant","content":[{"type":"image"}],"metadata":{}}}`, events.ErrKindUnmapped, "image"},
		{"request without a call", `{"type":"message","message":{"role":"assistant","content":[{"type":"toolRequest","id":"c","toolCall":{"status":"error","value":"bad"}}],"metadata":{}}}`, events.ErrKindUnmapped, "toolRequest"},
		{"malformed json", `{"type":"message",`, events.ErrKindParse, "malformed json"},
		{"message body of the wrong type", `{"type":"message","message":"x"}`, events.ErrKindParse, "message"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// A parse failure does not stop the stream: a good line follows.
			got := parseString(t, tt.line+"\n"+`{"type":"complete","total_tokens":0,"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0,"cache_write_input_tokens":0}`+"\n")
			wantKinds(t, got, events.KindError, events.KindSessionEnded)
			d := got[0].Data.(events.ErrorData)
			if d.Kind != tt.wantKind || !strings.Contains(d.Message, tt.wantMsg) {
				t.Errorf("error = %+v, want kind %q containing %q", d, tt.wantKind, tt.wantMsg)
			}
			if tt.wantKind == events.ErrKindUnmapped && len(got[0].Raw) == 0 {
				t.Error("unmapped error dropped the raw line")
			}
		})
	}
}

func TestParseSkipsLinesThatAreNotJSONObjects(t *testing.T) {
	t.Parallel()
	got := parseString(t, "goose banner\n\n"+`{"type":"complete","total_tokens":0,"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0,"cache_write_input_tokens":0}`+"\n")
	wantKinds(t, got, events.KindSessionEnded)
}

// Ollama reports no cost and nothing here claims one.
func TestParseSessionEndedClaimsNoCost(t *testing.T) {
	t.Parallel()
	got := parseFixture(t, "R5.stdout.jsonl")
	end := ended(t, got[len(got)-1])
	if end.Usage.Cost != nil {
		t.Errorf("cost = %v, want nil", *end.Usage.Cost)
	}
}

func TestParseStopsOnCancelledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := NewParser(Config{})
	err := p.Parse(ctx, strings.NewReader(`{"type":"surprise"}`+"\n"), func(events.Event) {})
	if err == nil {
		t.Fatal("Parse returned nil on a cancelled context")
	}
}
