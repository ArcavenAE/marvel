package panestate

import (
	"strings"
	"testing"
)

func tl(s string) []byte { return []byte(s) }

const (
	authErr = `{"type":"assistant","uuid":"u-err","timestamp":"2026-11-05T01:00:00Z","version":"2.1.287","isApiErrorMessage":true,"error":"authentication_failed","message":{"content":[{"type":"text","text":"Login expired"}]}}`
	reply   = `{"type":"assistant","uuid":"u-ok","timestamp":"2026-11-05T01:05:00Z","version":"2.1.287","message":{"content":[{"type":"text","text":"done"}]}}`
	user    = `{"type":"user","uuid":"u-user","timestamp":"2026-11-05T01:02:00Z","message":{"content":"hello"}}`
	otherEr = `{"type":"assistant","uuid":"u-rate","timestamp":"2026-11-05T01:03:00Z","version":"2.1.287","isApiErrorMessage":true,"error":"rate_limit"}`
)

func TestTranscriptNewestAssistantErrorIsAuthFailed(t *testing.T) {
	got := ClassifyClaudeTranscript([][]byte{tl(reply), tl(user), tl(authErr)})
	want := TranscriptReading{Verdict: TranscriptAuthFailed, UUID: "u-err", Timestamp: "2026-11-05T01:00:00Z", Version: "2.1.287"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestTranscriptALaterReplyAnswers(t *testing.T) {
	got := ClassifyClaudeTranscript([][]byte{tl(authErr), tl(reply)})
	if got.Verdict != TranscriptAnswered || got.UUID != "u-ok" {
		t.Fatalf("got %+v", got)
	}
}

func TestTranscriptAnotherKindOfErrorAnswers(t *testing.T) {
	got := ClassifyClaudeTranscript([][]byte{tl(authErr), tl(otherEr)})
	if got.Verdict != TranscriptAnswered {
		t.Fatalf("got %+v", got)
	}
}

func TestTranscriptAUserEntryDecidesNothing(t *testing.T) {
	if got := ClassifyClaudeTranscript([][]byte{tl(user)}); got.Verdict != TranscriptNone {
		t.Fatalf("user only: %+v", got)
	}
	if got := ClassifyClaudeTranscript([][]byte{tl(authErr), tl(user)}); got.Verdict != TranscriptAuthFailed {
		t.Fatalf("a user entry after the error cleared it: %+v", got)
	}
}

func TestTranscriptSkipsLinesThatDoNotDecode(t *testing.T) {
	got := ClassifyClaudeTranscript([][]byte{tl(authErr), tl(`{"type":"assistant","uuid":`), tl(``), tl(`not json`)})
	if got.Verdict != TranscriptAuthFailed {
		t.Fatalf("got %+v", got)
	}
}

func TestTranscriptAnErrorFieldThatIsNotAStringStillAnswers(t *testing.T) {
	odd := `{"type":"assistant","uuid":"u-odd","timestamp":"2026-11-05T01:04:00Z","isApiErrorMessage":true,"error":{"code":1}}`
	got := ClassifyClaudeTranscript([][]byte{tl(authErr), tl(odd)})
	if got.Verdict != TranscriptAnswered || got.UUID != "u-odd" {
		t.Fatalf("got %+v", got)
	}
}

func TestTranscriptAnErrorWithoutTheFlagIsNotAnApiError(t *testing.T) {
	unflagged := `{"type":"assistant","uuid":"u-x","error":"authentication_failed"}`
	if got := ClassifyClaudeTranscript([][]byte{tl(unflagged)}); got.Verdict != TranscriptAnswered {
		t.Fatalf("got %+v", got)
	}
}

// The reading carries typed fields only, so the prose in message reaches
// nothing it returns.
func TestTranscriptReadingNeverHoldsTheMessage(t *testing.T) {
	secret := `{"type":"assistant","uuid":"u-s","timestamp":"t","isApiErrorMessage":true,"error":"authentication_failed","message":{"content":[{"type":"text","text":"sk-ant-api03-SECRETVALUE"}]}}`
	got := ClassifyClaudeTranscript([][]byte{tl(secret)})
	if strings.Contains(strings.Join([]string{string(got.Verdict), got.UUID, got.Timestamp, got.Version}, "|"), "SECRET") {
		t.Fatalf("the reading holds message text: %+v", got)
	}
}
