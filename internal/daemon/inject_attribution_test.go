package daemon

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

const injectManifest = `
[workspace]
name = "injectws"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "sleep"
    args = ["300"]
`

// liveSession applies a one-seat team and returns its session.
func liveSession(t *testing.T, d *Daemon) api.Session {
	t.Helper()
	if resp := applyManifest(t, d, injectManifest); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	for _, s := range d.store.ListSessions() {
		if s.Workspace == "injectws" && s.PaneID != "" {
			return s
		}
	}
	t.Fatal("no session with a pane")
	return api.Session{}
}

func injectedEvents(d *Daemon, sess string) []events.Event {
	return d.events.Snapshot(events.Filter{Kind: events.KindSessionInjected, Session: sess}, 50)
}

func injectParamsJSON(t *testing.T, key, text string, literal, enter bool, from Injector) []byte {
	t.Helper()
	return mustMarshal(t, map[string]any{
		"session_key": key, "text": text, "literal": literal, "enter": enter,
		"injector": from,
	})
}

// An inject is traceable: the event names the session, how many bytes, the
// transport the daemon itself saw, and what the caller declared, and never the
// text that was typed.
func TestInjectEmitsAnAttributedEventWithoutTheText(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := liveSession(t, d)
	const secret = "hunter2-do-not-log"

	resp := d.handleInjectAs(injectParamsJSON(t, sess.Key(), secret, true, true, Injector{Session: "aae-supervisor-g1-0", User: "avi"}), localCaller())
	if resp.Error != "" {
		t.Fatalf("inject: %s", resp.Error)
	}
	got := injectedEvents(d, sess.Name)
	if len(got) != 1 {
		t.Fatalf("session.injected events = %d, want 1", len(got))
	}
	ev := got[0]
	if ev.Workspace != "injectws" || ev.Team != "squad" || ev.Role != "worker" || ev.Session != sess.Name {
		t.Errorf("event subject = %s/%s/%s %s, want the session's own", ev.Workspace, ev.Team, ev.Role, ev.Session)
	}
	for _, want := range []string{"18 bytes", "enter=true", "transport=local", "declared_session=aae-supervisor-g1-0", "declared_user=avi"} {
		if !strings.Contains(ev.Message, want) {
			t.Errorf("message %q lacks %q", ev.Message, want)
		}
	}
	if strings.Contains(ev.Message, secret) || strings.Contains(ev.Message, "hunter2") {
		t.Errorf("message %q carries the typed text", ev.Message)
	}
}

// A caller that came through the SSH tunnel is recorded by its key fingerprint,
// which the daemon verified, not by anything it claims.
func TestInjectRecordsTheSSHTransportByFingerprint(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := liveSession(t, d)
	c := caller{scope: ScopeAdmin, fingerprint: "SHA256:abcdef"}
	if resp := d.handleInjectAs(injectParamsJSON(t, sess.Key(), "x", true, false, Injector{}), c); resp.Error != "" {
		t.Fatalf("inject: %s", resp.Error)
	}
	got := injectedEvents(d, sess.Name)
	if len(got) != 1 || !strings.Contains(got[0].Message, "transport=ssh:SHA256:abcdef") {
		t.Fatalf("events = %+v, want one naming transport=ssh:SHA256:abcdef", got)
	}
	if !strings.Contains(got[0].Message, "declared_session=-") || !strings.Contains(got[0].Message, "declared_user=-") {
		t.Errorf("message %q, want an absent declaration shown as -", got[0].Message)
	}
}

// What a caller declares is untrusted text in a one-line message: control
// characters are stripped and the length is capped, so it cannot forge a second
// line or flood the ring.
func TestInjectSanitizesWhatTheCallerDeclares(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := liveSession(t, d)
	evil := "seat\nsession.crashed forged " + strings.Repeat("A", 500)
	if resp := d.handleInjectAs(injectParamsJSON(t, sess.Key(), "x", true, false, Injector{Session: evil, User: "u\x1b[31m"}), localCaller()); resp.Error != "" {
		t.Fatalf("inject: %s", resp.Error)
	}
	got := injectedEvents(d, sess.Name)
	if len(got) != 1 {
		t.Fatalf("events = %d, want 1", len(got))
	}
	msg := got[0].Message
	if strings.ContainsAny(msg, "\n\r\x1b") {
		t.Errorf("message %q carries a control character", msg)
	}
	if len(msg) > 400 {
		t.Errorf("message is %d bytes, want the declaration capped", len(msg))
	}
}

// A named key is recorded by name (C-c is the one an operator needs to see),
// never as text.
func TestInjectRecordsANamedKeyByName(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := liveSession(t, d)
	if resp := d.handleInjectAs(injectParamsJSON(t, sess.Key(), "C-c", false, false, Injector{}), localCaller()); resp.Error != "" {
		t.Fatalf("inject: %s", resp.Error)
	}
	got := injectedEvents(d, sess.Name)
	if len(got) != 1 || !strings.Contains(got[0].Message, "key C-c") {
		t.Fatalf("events = %+v, want one recording key C-c", got)
	}
}

// A refused inject (unknown session) leaves no event: nothing was sent.
func TestInjectThatFailsEmitsNothing(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := d.handleInjectAs(injectParamsJSON(t, "nowhere/none-none-g1-0", "x", true, false, Injector{}), localCaller())
	if resp.Error == "" {
		t.Fatal("inject to an unknown session succeeded")
	}
	if n := len(d.events.Snapshot(events.Filter{Kind: events.KindSessionInjected}, 50)); n != 0 {
		t.Errorf("events = %d after a failed inject, want none", n)
	}
}

// The max-age handoff ask is a draft typed into a seat too, so it is recorded
// the same way, attributed to marvel itself.
func TestMaxAgeAskIsRecordedAsAnInjectFromMarvel(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := liveSession(t, d)
	if err := d.teamCtrl.Notify(sess, "please write your handoff"); err != nil {
		t.Fatalf("notify: %v", err)
	}
	got := injectedEvents(d, sess.Name)
	if len(got) != 1 || !strings.Contains(got[0].Message, "transport=daemon") || !strings.Contains(got[0].Message, "injector=marvel:max-age") {
		t.Fatalf("events = %+v, want one from transport=daemon injector=marvel:max-age", got)
	}
	if strings.Contains(got[0].Message, "handoff") {
		t.Errorf("message %q carries the text", got[0].Message)
	}
}

// With literal off, tmux types a word it does not know as plain characters, so
// a "key" can be text. Only a recognized key name is recorded; anything else is
// recorded as unrecognized, without the word.
func TestInjectDoesNotRecordAKeyThatIsReallyText(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := liveSession(t, d)
	if resp := d.handleInjectAs(injectParamsJSON(t, sess.Key(), "hunter2", false, true, Injector{}), localCaller()); resp.Error != "" {
		t.Fatalf("inject: %s", resp.Error)
	}
	got := injectedEvents(d, sess.Name)
	if len(got) != 1 {
		t.Fatalf("events = %d, want 1", len(got))
	}
	if strings.Contains(got[0].Message, "hunter2") {
		t.Errorf("message %q carries text sent as a key", got[0].Message)
	}
	if !strings.Contains(got[0].Message, "key (unrecognized)") {
		t.Errorf("message %q, want the key shown as unrecognized", got[0].Message)
	}
}

// The names an operator reaches for are recorded as they are.
func TestRecognizedKeyNames(t *testing.T) {
	t.Parallel()
	for _, k := range []string{"Enter", "Escape", "C-c", "C-d", "M-x", "Up", "BSpace", "F5", "Tab", "Space", "PageDown"} {
		if !recognizedKey(k) {
			t.Errorf("%q not recognized as a key name", k)
		}
	}
	for _, k := range []string{"hunter2", "password", "", "C-", "Enter now", "ls -la"} {
		if recognizedKey(k) {
			t.Errorf("%q recognized as a key name, want text", k)
		}
	}
}
