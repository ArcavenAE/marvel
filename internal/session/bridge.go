package session

import (
	"fmt"
	"strings"

	"github.com/arcavenae/marvel/internal/events"
	rtevents "github.com/arcavenae/marvel/internal/runtime/events"
)

// coords is the session identity stamped onto every agent event. Held by
// value so the bridge goroutine does not read an *api.Session that the
// store may be mutating underneath it.
type coords struct {
	Workspace string
	Team      string
	Role      string
	Session   string
}

// bridgeEvent lifts one normalized adapter event into a ring event. The
// adapter vocabulary is rich and typed; the ring is a flat operator log,
// so the payload becomes a one-line summary. Detail that operators need
// verbatim (full tool inputs, message bodies) is not the ring's job — it
// stays in the adapter stream for consumers that subscribe to it.
func bridgeEvent(c coords, ev rtevents.Event) events.Event {
	kind, sev := ringKind(ev)
	return events.Event{
		Timestamp: ev.TS,
		Kind:      kind,
		Severity:  sev,
		Workspace: c.Workspace,
		Team:      c.Team,
		Role:      c.Role,
		Session:   c.Session,
		Message:   summarize(ev),
	}
}

// ringKind maps an adapter kind to its ring kind and severity. An
// unrecognized adapter kind still lands in the ring, tagged as an agent
// error, rather than disappearing.
func ringKind(ev rtevents.Event) (events.Kind, events.Severity) {
	switch ev.Event {
	case rtevents.KindSessionStarted:
		return events.KindAgentSessionStarted, events.SeverityInfo
	case rtevents.KindSessionEnded:
		if d, ok := ev.Data.(rtevents.SessionEndedData); ok && d.ExitCode != 0 {
			return events.KindAgentSessionEnded, events.SeverityWarning
		}
		return events.KindAgentSessionEnded, events.SeverityInfo
	case rtevents.KindTurnStarted:
		return events.KindAgentTurnStarted, events.SeverityInfo
	case rtevents.KindTurnCompleted:
		return events.KindAgentTurnCompleted, events.SeverityInfo
	case rtevents.KindMessageDelta:
		return events.KindAgentMessageDelta, events.SeverityInfo
	case rtevents.KindMessageCompleted:
		return events.KindAgentMessageCompleted, events.SeverityInfo
	case rtevents.KindToolCall:
		return events.KindAgentToolCall, events.SeverityInfo
	case rtevents.KindToolResult:
		if d, ok := ev.Data.(rtevents.ToolResultData); ok && !d.OK {
			return events.KindAgentToolResult, events.SeverityWarning
		}
		return events.KindAgentToolResult, events.SeverityInfo
	case rtevents.KindPermissionRequested:
		return events.KindAgentPermissionRequested, events.SeverityWarning
	case rtevents.KindAuthRequired:
		return events.KindAgentAuthRequired, events.SeverityWarning
	case rtevents.KindHealthHeartbeat:
		return events.KindAgentHealthHeartbeat, events.SeverityInfo
	case rtevents.KindError:
		return events.KindAgentError, events.SeverityWarning
	default:
		return events.KindAgentError, events.SeverityWarning
	}
}

// maxRingMessage keeps one event to one terminal line in `marvel events`.
const maxRingMessage = 160

func summarize(ev rtevents.Event) string {
	switch d := ev.Data.(type) {
	case rtevents.SessionStartedData:
		s := "model " + orUnknown(d.Model)
		if d.Cwd != "" {
			s += " in " + d.Cwd
		}
		if d.Resumed {
			s += " (resumed)"
		}
		return oneLine(s)
	case rtevents.SessionEndedData:
		return oneLine(sessionEndedSummary(d))
	case rtevents.TurnData:
		return oneLine(fmt.Sprintf("tokens in=%d out=%d", d.UsageDelta.In, d.UsageDelta.Out))
	case rtevents.MessageData:
		if d.Text == "" {
			return oneLine(d.Role)
		}
		return oneLine(d.Role + ": " + d.Text)
	case rtevents.ToolCallData:
		s := d.Tool
		if d.CallID != "" {
			s += " (" + d.CallID + ")"
		}
		return oneLine(s)
	case rtevents.ToolResultData:
		verdict := "ok"
		if !d.OK {
			verdict = "error"
		}
		if d.CallID != "" {
			return oneLine(verdict + " (" + d.CallID + ")")
		}
		return verdict
	case rtevents.PermissionRequestedData:
		s := d.Action
		if d.Detail != "" {
			s += ": " + d.Detail
		}
		return oneLine(s)
	case rtevents.AuthRequiredData:
		return oneLine(d.Hint)
	case rtevents.HealthHeartbeatData:
		return oneLine(d.State)
	case rtevents.ErrorData:
		if d.Message != "" {
			return oneLine(d.Kind + ": " + d.Message)
		}
		return oneLine(d.Kind)
	default:
		return string(ev.Event)
	}
}

// sessionEndedSummary is the metering line. Promoting the vendor's
// accounting fields into the vocabulary is only useful if an operator can
// see them, so cost, timings, and turn count go on the one line that
// `marvel events` shows.
func sessionEndedSummary(d rtevents.SessionEndedData) string {
	var b strings.Builder
	fmt.Fprintf(&b, "exit %d", d.ExitCode)
	if d.Reason != "" {
		fmt.Fprintf(&b, " (%s)", d.Reason)
	}
	fmt.Fprintf(&b, " tokens in=%d out=%d", d.Usage.In, d.Usage.Out)
	if d.Usage.Cost != nil {
		fmt.Fprintf(&b, " cost=$%.4f", *d.Usage.Cost)
	}
	if m := d.Metering; m != nil {
		if m.NumTurns > 0 {
			fmt.Fprintf(&b, " turns=%d", m.NumTurns)
		}
		if m.DurationMS > 0 {
			fmt.Fprintf(&b, " dur=%dms", m.DurationMS)
		}
		if m.APIDurationMS > 0 {
			fmt.Fprintf(&b, " api=%dms", m.APIDurationMS)
		}
		if m.TTFTMS > 0 {
			fmt.Fprintf(&b, " ttft=%dms", m.TTFTMS)
		}
		if n := len(m.PermissionDenials); n > 0 {
			fmt.Fprintf(&b, " denials=%d", n)
		}
	}
	return b.String()
}

// oneLine collapses whitespace and clips to one terminal line. Adapter
// payloads are already bounded at 64 KiB; the ring wants far less.
func oneLine(s string) string {
	return rtevents.TruncateString(strings.Join(strings.Fields(s), " "), maxRingMessage)
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
