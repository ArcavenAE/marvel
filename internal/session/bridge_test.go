package session

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/events"
	rtevents "github.com/arcavenae/marvel/internal/runtime/events"
)

func testCoords() coords {
	return coords{Workspace: "acme", Team: "squad", Role: "worker", Session: "acme/squad-worker-g1-0"}
}

func TestBridgeEventTagsSessionCoordinates(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	got := bridgeEvent(testCoords(), rtevents.Event{
		Event: rtevents.KindToolCall,
		TS:    ts,
		Data:  rtevents.ToolCallData{Tool: "Bash", CallID: "toolu_1"},
	})

	if got.Kind != events.KindAgentToolCall {
		t.Errorf("kind = %q, want %q", got.Kind, events.KindAgentToolCall)
	}
	if got.Workspace != "acme" || got.Team != "squad" || got.Role != "worker" {
		t.Errorf("coordinates lost: %+v", got)
	}
	if got.Session != "acme/squad-worker-g1-0" {
		t.Errorf("session = %q", got.Session)
	}
	if !got.Timestamp.Equal(ts) {
		t.Errorf("timestamp = %v, want %v", got.Timestamp, ts)
	}
	if got.Message != "Bash (toolu_1)" {
		t.Errorf("message = %q", got.Message)
	}
}

func TestBridgeEventKindAndSeverity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		in       rtevents.Event
		wantKind events.Kind
		wantSev  events.Severity
	}{
		{
			name:     "session started",
			in:       rtevents.Event{Event: rtevents.KindSessionStarted, Data: rtevents.SessionStartedData{Model: "haiku"}},
			wantKind: events.KindAgentSessionStarted,
			wantSev:  events.SeverityInfo,
		},
		{
			name:     "clean exit is info",
			in:       rtevents.Event{Event: rtevents.KindSessionEnded, Data: rtevents.SessionEndedData{ExitCode: 0}},
			wantKind: events.KindAgentSessionEnded,
			wantSev:  events.SeverityInfo,
		},
		{
			name:     "nonzero exit is a warning",
			in:       rtevents.Event{Event: rtevents.KindSessionEnded, Data: rtevents.SessionEndedData{ExitCode: 1}},
			wantKind: events.KindAgentSessionEnded,
			wantSev:  events.SeverityWarning,
		},
		{
			name:     "failed tool result is a warning",
			in:       rtevents.Event{Event: rtevents.KindToolResult, Data: rtevents.ToolResultData{CallID: "t1", OK: false}},
			wantKind: events.KindAgentToolResult,
			wantSev:  events.SeverityWarning,
		},
		{
			name:     "permission request is a warning",
			in:       rtevents.Event{Event: rtevents.KindPermissionRequested, Data: rtevents.PermissionRequestedData{Action: "tool.Bash"}},
			wantKind: events.KindAgentPermissionRequested,
			wantSev:  events.SeverityWarning,
		},
		{
			name:     "auth required is a warning",
			in:       rtevents.Event{Event: rtevents.KindAuthRequired, Data: rtevents.AuthRequiredData{Hint: "run /login"}},
			wantKind: events.KindAgentAuthRequired,
			wantSev:  events.SeverityWarning,
		},
		{
			name:     "adapter error is a warning",
			in:       rtevents.Event{Event: rtevents.KindError, Data: rtevents.ErrorData{Kind: rtevents.ErrKindParse, Message: "bad line"}},
			wantKind: events.KindAgentError,
			wantSev:  events.SeverityWarning,
		},
		{
			name:     "unknown adapter kind still lands",
			in:       rtevents.Event{Event: rtevents.Kind("message.thinking")},
			wantKind: events.KindAgentError,
			wantSev:  events.SeverityWarning,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := bridgeEvent(testCoords(), tc.in)
			if got.Kind != tc.wantKind {
				t.Errorf("kind = %q, want %q", got.Kind, tc.wantKind)
			}
			if got.Severity != tc.wantSev {
				t.Errorf("severity = %q, want %q", got.Severity, tc.wantSev)
			}
		})
	}
}

func TestBridgeSessionEndedSummaryCarriesMetering(t *testing.T) {
	t.Parallel()
	cost := 0.0421
	got := bridgeEvent(testCoords(), rtevents.Event{
		Event: rtevents.KindSessionEnded,
		Data: rtevents.SessionEndedData{
			Reason:   "end_turn",
			ExitCode: 0,
			Usage:    rtevents.Usage{In: 11368, Out: 13, Cost: &cost},
			Metering: &rtevents.Metering{
				DurationMS:        4816,
				APIDurationMS:     6546,
				TTFTMS:            4775,
				NumTurns:          1,
				PermissionDenials: []rtevents.PermissionDenial{{Tool: "Bash"}},
			},
		},
	})

	for _, want := range []string{
		"exit 0", "(end_turn)", "in=11368", "out=13", "cost=$0.0421",
		"turns=1", "dur=4816ms", "api=6546ms", "ttft=4775ms", "denials=1",
	} {
		if !strings.Contains(got.Message, want) {
			t.Errorf("message missing %q: %s", want, got.Message)
		}
	}
}

func TestBridgeMessageSummaryIsOneClippedLine(t *testing.T) {
	t.Parallel()
	got := bridgeEvent(testCoords(), rtevents.Event{
		Event: rtevents.KindMessageCompleted,
		Data: rtevents.MessageData{
			Role: "assistant",
			Text: "first line\nsecond line " + strings.Repeat("x", 400),
		},
	})
	if strings.ContainsAny(got.Message, "\n\r") {
		t.Errorf("message spans lines: %q", got.Message)
	}
	if len(got.Message) > maxRingMessage {
		t.Errorf("message is %d bytes, want at most %d", len(got.Message), maxRingMessage)
	}
	if !strings.HasPrefix(got.Message, "assistant: first line second line") {
		t.Errorf("message lost its head: %q", got.Message)
	}
}
