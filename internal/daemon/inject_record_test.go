package daemon

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// recordedInject runs recordInject on a scratch ring and returns the message it
// emitted.
func recordedInject(t *testing.T, p injectParams, clearKey string) string {
	t.Helper()
	ring := events.NewRing(8)
	sess := api.Session{Name: "squad-worker-g1-0", Workspace: "ws", Team: "squad", Role: "worker"}
	recordInject(ring, sess, p, clearKey, "transport=local")
	got := ring.Snapshot(events.Filter{Kind: events.KindSessionInjected}, 8)
	if len(got) != 1 {
		t.Fatalf("session.injected events = %d, want 1", len(got))
	}
	return got[0].Message
}

// The record says what reached the pane. Non-empty literal text is pasted as
// is, so its byte count stands. An empty literal text with Enter sends one key
// and no bytes, so it reads as that key, not as "0 bytes". A clear key goes out
// before the text and is named. The typed text is never recorded.
func TestRecordInjectCountsWhatWasSent(t *testing.T) {
	for _, tc := range []struct {
		name     string
		p        injectParams
		clearKey string
		want     []string
		absent   []string
	}{
		{
			"text and enter",
			injectParams{Text: "hunter2-do-not-log", Literal: true, Enter: true},
			"",
			[]string{"18 bytes literal=true", "enter=true"},
			[]string{"key Enter", "clear=", "hunter2"},
		},
		{
			"text without enter",
			injectParams{Text: "abc", Literal: true},
			"",
			[]string{"3 bytes literal=true", "enter=false"},
			[]string{"key Enter"},
		},
		{
			"empty text with enter is one key",
			injectParams{Literal: true, Enter: true},
			"",
			[]string{"key Enter", "enter=true"},
			[]string{"0 bytes"},
		},
		{
			"empty text without enter sent nothing",
			injectParams{Literal: true},
			"",
			[]string{"0 bytes literal=true", "enter=false"},
			[]string{"key Enter"},
		},
		{
			"named key",
			injectParams{Text: "Escape"},
			"",
			[]string{"key Escape", "enter=false"},
			[]string{"bytes", "clear="},
		},
		{
			"clear key then text",
			injectParams{Text: "hello", Literal: true, Enter: true},
			"C-u",
			[]string{"5 bytes literal=true", "enter=true", "clear=C-u"},
			nil,
		},
		{
			"clear key then empty enter",
			injectParams{Literal: true, Enter: true},
			"C-u",
			[]string{"key Enter", "clear=C-u"},
			[]string{"0 bytes"},
		},
	} {
		msg := recordedInject(t, tc.p, tc.clearKey)
		for _, w := range tc.want {
			if !strings.Contains(msg, w) {
				t.Errorf("%s: message %q lacks %q", tc.name, msg, w)
			}
		}
		for _, a := range tc.absent {
			if strings.Contains(msg, a) {
				t.Errorf("%s: message %q carries %q", tc.name, msg, a)
			}
		}
	}
}

// Through the handler: `inject <session> ” --enter` sends only the Enter key,
// and the event says so.
func TestInjectEmptyTextWithEnterRecordsTheKey(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := liveSession(t, d)
	resp := d.handleInjectAs(injectParamsJSON(t, sess.Key(), "", true, true, Injector{}), localCaller())
	if resp.Error != "" {
		t.Fatalf("inject: %s", resp.Error)
	}
	got := injectedEvents(d, sess.Name)
	if len(got) != 1 {
		t.Fatalf("session.injected events = %d, want 1", len(got))
	}
	if !strings.Contains(got[0].Message, "key Enter") || strings.Contains(got[0].Message, "0 bytes") {
		t.Errorf("message %q, want the Enter key and no byte count", got[0].Message)
	}
}
