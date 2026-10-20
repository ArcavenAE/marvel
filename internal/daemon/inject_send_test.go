package daemon

import (
	"errors"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// failingKeys is a keySender that records its calls (sendCall, limitaction_test.go)
// and fails the ones failOn picks, so a test can make each send step fail in turn.
type failingKeys struct {
	calls  []sendCall
	failOn func(c sendCall) error
}

func (f *failingKeys) SendKeys(pane, text string, literal, enter bool) error {
	c := sendCall{pane, text, literal, enter}
	f.calls = append(f.calls, c)
	if f.failOn != nil {
		return f.failOn(c)
	}
	return nil
}

var errSendFailed = errors.New("tmux refused")

func injectedCount(ring *events.Ring) int {
	return len(ring.Snapshot(events.Filter{Kind: events.KindSessionInjected}, 8))
}

// An inject is recorded only when its keys reached the pane. A failed clear, a
// failed text send after a clear, a failed text send with no clear, and a failed
// Enter (which goes out in the same call as the text) each leave no
// session.injected event, and the error names what did and did not go out.
func TestSendInjectRecordsOnlyWhatReachedThePane(t *testing.T) {
	failClear := func(c sendCall) error {
		if !c.literal && !c.enter && c.text == "C-u" {
			return errSendFailed
		}
		return nil
	}
	failText := func(c sendCall) error {
		if c.literal {
			return errSendFailed
		}
		return nil
	}
	failEnter := func(c sendCall) error {
		if c.enter {
			return errSendFailed
		}
		return nil
	}
	for _, tc := range []struct {
		name      string
		p         injectParams
		clearKey  string
		failOn    func(sendCall) error
		wantCalls int
		wantErr   string
		wantEvent bool
	}{
		{"clear fails", injectParams{Text: "hello", Literal: true, Enter: true}, "C-u", failClear, 1, "clear: tmux refused", false},
		{"text fails after a clear", injectParams{Text: "hello", Literal: true}, "C-u", failText, 2, "already sent", false},
		{"text fails with no clear", injectParams{Text: "hello", Literal: true}, "", failText, 1, "tmux refused", false},
		{"enter fails after the text", injectParams{Text: "hello", Literal: true, Enter: true}, "", failEnter, 1, "tmux refused", false},
		{"enter fails after a clear", injectParams{Literal: true, Enter: true}, "C-u", failEnter, 2, "already sent", false},
		{"clear and text both go out", injectParams{Text: "hello", Literal: true, Enter: true}, "C-u", nil, 2, "", true},
		{"empty text with enter goes out", injectParams{Literal: true, Enter: true}, "", nil, 1, "", true},
	} {
		ring := events.NewRing(8)
		drv := &failingKeys{failOn: tc.failOn}
		sess := api.Session{Name: "squad-worker-g1-0", PaneID: "%3"}
		err := sendInject(drv, ring, sess, tc.p, tc.clearKey, "transport=local")
		if len(drv.calls) != tc.wantCalls {
			t.Errorf("%s: %d send calls %v, want %d", tc.name, len(drv.calls), drv.calls, tc.wantCalls)
		}
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("%s: error %v, want none", tc.name, err)
		case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
			t.Errorf("%s: error %v, want one naming %q", tc.name, err, tc.wantErr)
		}
		if got := injectedCount(ring); (got == 1) != tc.wantEvent || got > 1 {
			t.Errorf("%s: %d session.injected events, want recorded=%v", tc.name, got, tc.wantEvent)
		}
	}
}

// The clear key goes out first, as a key and not literal text, and the text
// after it; the record names both.
func TestSendInjectSendsTheClearBeforeTheText(t *testing.T) {
	ring := events.NewRing(8)
	drv := &failingKeys{}
	sess := api.Session{Name: "squad-worker-g1-0", PaneID: "%3"}
	p := injectParams{Text: "hello", Literal: true, Enter: true}
	if err := sendInject(drv, ring, sess, p, "C-u", "transport=local"); err != nil {
		t.Fatalf("sendInject: %v", err)
	}
	want := []sendCall{{"%3", "C-u", false, false}, {"%3", "hello", true, true}}
	if len(drv.calls) != 2 || drv.calls[0] != want[0] || drv.calls[1] != want[1] {
		t.Fatalf("calls = %v, want %v", drv.calls, want)
	}
	msg := ring.Snapshot(events.Filter{Kind: events.KindSessionInjected}, 8)[0].Message
	if !strings.Contains(msg, "5 bytes literal=true") || !strings.Contains(msg, "clear=C-u") {
		t.Errorf("record %q, want the byte count and the clear key", msg)
	}
}
