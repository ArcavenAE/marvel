package panestate

import "encoding/json"

// TranscriptVerdict is what the newest assistant entry in a transcript says.
type TranscriptVerdict string

const (
	// TranscriptNone: no assistant entry among the lines, so nothing decides.
	TranscriptNone TranscriptVerdict = ""
	// TranscriptAuthFailed: the newest assistant entry is an API error whose
	// error is authentication_failed.
	TranscriptAuthFailed TranscriptVerdict = "auth-failed"
	// TranscriptAnswered: the newest assistant entry is anything else, a reply
	// or another kind of API error.
	TranscriptAnswered TranscriptVerdict = "answered"
)

// TranscriptReading is the verdict and the entry that gave it. Only typed
// fields are ever read, so nothing here can carry the harness's prose.
type TranscriptReading struct {
	Verdict   TranscriptVerdict
	UUID      string
	Timestamp string
	Version   string
}

// transcriptEntry is every field the reader decodes. There is no message
// field: the harness's prose is never decoded, kept or logged (docs/design/
// harness-state-watchdog-p1.md section 4a), the same limit the codex rollout
// reader keeps. error stays raw so an entry whose error is not a string still
// decodes and still counts as the newest assistant entry.
type transcriptEntry struct {
	Type              string          `json:"type"`
	IsAPIErrorMessage bool            `json:"isApiErrorMessage"`
	Error             json.RawMessage `json:"error"`
	Timestamp         string          `json:"timestamp"`
	UUID              string          `json:"uuid"`
	Version           string          `json:"version"`
}

// authFailedError is the typed error claude records when the login is gone.
const authFailedError = "authentication_failed"

// ClassifyClaudeTranscript reads claude's transcript lines, oldest first. Only
// assistant entries decide, and the newest one decides: an API error whose
// error is authentication_failed reads auth-failed, and any other assistant
// entry reads answered. A user entry neither sets nor clears, so a nudge into
// a logged-out seat leaves it logged out until the seat replies. A line that
// does not decode is skipped.
func ClassifyClaudeTranscript(lines [][]byte) TranscriptReading {
	var out TranscriptReading
	for _, line := range lines {
		var e transcriptEntry
		if err := json.Unmarshal(line, &e); err != nil || e.Type != "assistant" {
			continue
		}
		out = TranscriptReading{Verdict: TranscriptAnswered, UUID: e.UUID, Timestamp: e.Timestamp, Version: e.Version}
		var name string
		if e.IsAPIErrorMessage && json.Unmarshal(e.Error, &name) == nil && name == authFailedError {
			out.Verdict = TranscriptAuthFailed
		}
	}
	return out
}
