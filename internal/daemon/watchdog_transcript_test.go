package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// The transcript source of the watchdog: docs/design/harness-state-watchdog-p1.md
// section 4a. Every fixture is a synthetic file with typed fields only; no
// real seat's transcript is read.

const txSession = "11111111-2222-3333-4444-555555555555"

type txRig struct {
	*wdRig
	cfg string
}

func newTXRig(t *testing.T) *txRig {
	t.Helper()
	return &txRig{wdRig: newWDRig(t), cfg: t.TempDir()}
}

// claude adds a claude seat that is heartbeating (worked a minute ago, so
// inside the quiet window) with a transcript id and a config dir of its own.
func (r *txRig) claude(name, sessionID string) {
	r.t.Helper()
	r.seat(name, "claude", "2.1.283", 30*time.Minute, time.Minute)
	err := r.store.UpdateSession("ws/"+name, func(s *api.Session) error {
		s.HarnessSessionID = sessionID
		s.Runtime.Env = map[string]string{"CLAUDE_CONFIG_DIR": r.cfg}
		return nil
	})
	if err != nil {
		r.t.Fatal(err)
	}
}

// write appends lines to the seat's transcript under a project dir.
func (r *txRig) write(project, id string, lines ...string) string {
	r.t.Helper()
	dir := filepath.Join(r.cfg, "projects", project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		r.t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		r.t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			r.t.Fatal(err)
		}
	}
	bump := time.Now().Add(time.Duration(len(lines)) * time.Second)
	_ = os.Chtimes(path, bump, bump)
	return path
}

const (
	txErr   = `{"type":"assistant","uuid":"u-err","timestamp":"2026-11-05T01:00:00Z","version":"2.1.287","isApiErrorMessage":true,"error":"authentication_failed","message":{"content":[{"type":"text","text":"Login expired"}]}}`
	txReply = `{"type":"assistant","uuid":"u-ok","timestamp":"2026-11-05T01:05:00Z","version":"2.1.287","message":{"content":[{"type":"text","text":"done"}]}}`
	txUser  = `{"type":"user","uuid":"u-user","timestamp":"2026-11-05T01:02:00Z","message":{"content":"hello"}}`
)

func TestTranscriptAuthErrorSetsLoggedOutAtHighFromTheTranscript(t *testing.T) {
	r := newTXRig(t)
	r.claude("a", txSession)
	r.write("-proj", txSession, txUser, txErr)
	r.w.Once()
	hs := r.get("a").HarnessState
	if hs == nil || hs.State != api.HarnessStateLoggedOut || hs.Confidence != "high" {
		t.Fatalf("harness state = %+v", hs)
	}
	if hs.Source != api.HarnessSourceTranscript || hs.PatternID != "transcript:authentication_failed" || hs.PatternVersion != 1 {
		t.Fatalf("source or pattern wrong: %+v", hs)
	}
	if hs.HarnessVersion != "2.1.287" || strings.Join(hs.Evidence, "|") != "entry u-err at 2026-11-05T01:00:00Z" {
		t.Fatalf("version or evidence wrong: %+v", hs)
	}
	if got := len(r.kinds(events.KindSessionHarnessState)); got != 1 {
		t.Fatalf("harness-state events = %d", got)
	}
	// The seat is inside the quiet window and no pane was read: the source does
	// not wait on the gate.
	if p := r.panes["%a"]; p.reads != 0 || p.caps != 0 {
		t.Fatalf("a pane was read: reads=%d caps=%d", p.reads, p.caps)
	}
}

func TestTranscriptALaterReplyClearsIt(t *testing.T) {
	r := newTXRig(t)
	r.claude("a", txSession)
	r.write("-proj", txSession, txErr)
	r.w.Once()
	r.write("-proj", txSession, txReply)
	r.w.Once()
	if hs := r.get("a").HarnessState; hs != nil {
		t.Fatalf("state after a reply = %+v", hs)
	}
	if got := len(r.kinds(events.KindSessionHarnessStateCleared)); got != 1 {
		t.Fatalf("cleared events = %d", got)
	}
}

func TestTranscriptALaterUserEntryAloneDoesNotClearIt(t *testing.T) {
	r := newTXRig(t)
	r.claude("a", txSession)
	r.write("-proj", txSession, txErr)
	r.w.Once()
	r.write("-proj", txSession, txUser)
	r.w.Once()
	if hs := r.get("a").HarnessState; hs == nil || hs.State != api.HarnessStateLoggedOut {
		t.Fatalf("state after a user entry = %+v", hs)
	}
}

// #801: every heartbeat stamps ContextAt, which clears any pane state and keeps
// the quiet gate shut. The transcript state must hold through that.
func TestTranscriptStateHoldsWhileHeartbeatsKeepArriving(t *testing.T) {
	r := newTXRig(t)
	r.claude("a", txSession)
	r.write("-proj", txSession, txErr)
	r.w.Once()
	for i := 0; i < 4; i++ {
		r.now = r.now.Add(15 * time.Second)
		r.setContextAt("ws/a", r.now)
		r.w.Once()
	}
	hs := r.get("a").HarnessState
	if hs == nil || hs.State != api.HarnessStateLoggedOut || hs.Source != api.HarnessSourceTranscript {
		t.Fatalf("state after heartbeats = %+v", hs)
	}
	if got := len(r.kinds(events.KindSessionHarnessState)); got != 1 {
		t.Fatalf("harness-state events = %d, want the one", got)
	}
	if got := len(r.kinds(events.KindSessionHarnessStateCleared)); got != 0 {
		t.Fatalf("cleared events = %d", got)
	}
}

func TestTranscriptGapsAreNamedAndSetNothing(t *testing.T) {
	r := newTXRig(t)
	r.claude("noid", "")
	r.claude("nofile", "99999999-aaaa-bbbb-cccc-dddddddddddd")
	r.claude("two", "22222222-aaaa-bbbb-cccc-dddddddddddd")
	r.write("-p1", "22222222-aaaa-bbbb-cccc-dddddddddddd", txErr)
	r.write("-p2", "22222222-aaaa-bbbb-cccc-dddddddddddd", txErr)
	r.w.Once()
	for name, want := range map[string]string{
		"noid":   "transcript: no assigned session id",
		"nofile": "transcript: no file for the session id",
		"two":    "transcript: several files for the session id",
	} {
		s := r.get(name)
		if s.HarnessState != nil {
			t.Errorf("%s: state = %+v", name, s.HarnessState)
		}
		if s.TranscriptGap != want {
			t.Errorf("%s: gap = %q, want %q", name, s.TranscriptGap, want)
		}
	}
}

func TestTranscriptGapClearsOnceTheFileAppears(t *testing.T) {
	r := newTXRig(t)
	r.claude("a", txSession)
	r.w.Once()
	if g := r.get("a").TranscriptGap; g == "" {
		t.Fatal("no gap while there is no file")
	}
	r.write("-proj", txSession, txReply)
	r.w.Once()
	if g := r.get("a").TranscriptGap; g != "" {
		t.Fatalf("gap = %q once the file is read", g)
	}
}

func TestTranscriptUnchangedFileIsNotReadAgain(t *testing.T) {
	r := newTXRig(t)
	r.claude("a", txSession)
	r.write("-proj", txSession, txErr)
	reads := 0
	real := r.w.tailRead
	r.w.tailRead = func(path string, from int64) ([]byte, error) {
		reads++
		return real(path, from)
	}
	r.w.Once()
	r.w.Once()
	r.w.Once()
	if reads != 1 {
		t.Fatalf("reads = %d, want 1 for a file that did not change", reads)
	}
	r.write("-proj", txSession, txUser)
	r.w.Once()
	if reads != 2 {
		t.Fatalf("reads = %d after the file grew, want 2", reads)
	}
}

func TestTranscriptReadsOnlyTheNewBytes(t *testing.T) {
	r := newTXRig(t)
	r.claude("a", txSession)
	path := r.write("-proj", txSession, txErr)
	var froms []int64
	real := r.w.tailRead
	r.w.tailRead = func(p string, from int64) ([]byte, error) {
		froms = append(froms, from)
		return real(p, from)
	}
	r.w.Once()
	fi, _ := os.Stat(path)
	r.write("-proj", txSession, txReply)
	r.w.Once()
	if len(froms) != 2 || froms[1] != fi.Size() {
		t.Fatalf("offsets = %v, want the second read to start at %d", froms, fi.Size())
	}
}

// A message that holds a secret-shaped string reaches no state field, event or
// log line.
func TestTranscriptMaskingNothingOfTheMessageSurfaces(t *testing.T) {
	r := newTXRig(t)
	r.claude("a", txSession)
	secret := "sk-ant-api03-SECRETSECRETSECRET"
	r.write("-proj", txSession,
		`{"type":"assistant","uuid":"u-s","timestamp":"2026-11-05T01:00:00Z","version":"2.1.287","isApiErrorMessage":true,"error":"authentication_failed","message":{"content":[{"type":"text","text":"`+secret+`"}]}}`)
	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(old) })
	r.w.Once()
	s := r.get("a")
	raw, _ := json.Marshal(s)
	evs, _ := json.Marshal(r.ring.Snapshot(events.Filter{}, 0))
	for what, got := range map[string]string{"session": string(raw), "events": string(evs), "log": logs.String()} {
		if strings.Contains(got, "SECRET") {
			t.Errorf("the message reached the %s: %s", what, got)
		}
	}
	if s.HarnessState == nil {
		t.Fatal("no state; the fixture did not read")
	}
}

func TestTranscriptWinsOverAPaneReadingForTheSameSeat(t *testing.T) {
	r := newTXRig(t)
	// Quiet past the window, so the pane path would capture; its screen is the
	// logged-out sample, which would read as a pane logged-out.
	r.claude("a", txSession)
	r.setContextAt("ws/a", r.now.Add(-30*time.Minute))
	r.write("-proj", txSession, txErr)
	r.w.Once()
	hs := r.get("a").HarnessState
	if hs == nil || hs.Source != api.HarnessSourceTranscript {
		t.Fatalf("state = %+v, want the transcript's", hs)
	}
	if p := r.panes["%a"]; p.caps != 0 {
		t.Fatalf("the pane was captured %d times", p.caps)
	}
}

func TestPanePickerStandsWhenTheTranscriptHasNoAuthError(t *testing.T) {
	r := newTXRig(t)
	r.claude("a", txSession)
	r.setContextAt("ws/a", r.now.Add(-30*time.Minute))
	r.write("-proj", txSession, txReply)
	r.w.Once()
	hs := r.get("a").HarnessState
	if hs == nil || hs.State != api.HarnessStateLoggedOut || hs.PatternID != "logged-out" {
		t.Fatalf("state = %+v, want the pane picker's", hs)
	}
	if hs.Source == api.HarnessSourceTranscript {
		t.Fatalf("the pane state carries the transcript source: %+v", hs)
	}
}

func TestTranscriptSkipsAClientWorkspaceSeat(t *testing.T) {
	r := newTXRig(t)
	r.claude("a", txSession)
	r.write("-proj", txSession, txErr)
	r.w.client = func(string) (bool, error) { return true, nil }
	reads := 0
	real := r.w.tailRead
	r.w.tailRead = func(p string, from int64) ([]byte, error) { reads++; return real(p, from) }
	r.w.Once()
	s := r.get("a")
	if s.HarnessState != nil || reads != 0 {
		t.Fatalf("a client seat was read: state=%+v reads=%d", s.HarnessState, reads)
	}
	if s.TranscriptGap != "transcript: client workspace, not read" {
		t.Fatalf("gap = %q", s.TranscriptGap)
	}
}

func TestTranscriptSkipsASeatWhoseWorkspaceCannotBeRead(t *testing.T) {
	r := newTXRig(t)
	r.claude("a", txSession)
	r.write("-proj", txSession, txErr)
	r.w.client = func(string) (bool, error) { return false, errors.New("no such workspace") }
	r.w.Once()
	if s := r.get("a"); s.HarnessState != nil || s.TranscriptGap != "transcript: client workspace, not read" {
		t.Fatalf("an unreadable workspace was read as not client: %+v", s)
	}
}

func TestTranscriptNeverReadsANonClaudeSeat(t *testing.T) {
	r := newTXRig(t)
	r.seat("c", "codex", "2.1.290", 30*time.Minute, time.Minute)
	_ = r.store.UpdateSession("ws/c", func(s *api.Session) error {
		s.HarnessSessionID = txSession
		s.Runtime.Env = map[string]string{"CLAUDE_CONFIG_DIR": r.cfg}
		return nil
	})
	r.write("-proj", txSession, txErr)
	r.w.Once()
	if s := r.get("c"); s.HarnessState != nil || s.TranscriptGap != "" {
		t.Fatalf("a codex seat was read: %+v", s)
	}
}

func TestTranscriptAnIdThatIsAPathIsRefused(t *testing.T) {
	r := newTXRig(t)
	r.claude("a", "../../etc/passwd")
	r.w.Once()
	if s := r.get("a"); s.HarnessState != nil || s.TranscriptGap == "" {
		t.Fatalf("a path-shaped id was used: %+v", s)
	}
}

func TestTranscriptStateEndsWithTheSession(t *testing.T) {
	r := newTXRig(t)
	r.claude("a", txSession)
	r.write("-proj", txSession, txErr)
	r.w.Once()
	_ = r.store.UpdateSession("ws/a", func(s *api.Session) error { s.State = api.SessionSucceeded; return nil })
	r.w.Once()
	if hs := r.get("a").HarnessState; hs != nil {
		t.Fatalf("state after the session ended = %+v", hs)
	}
	if _, ok := r.w.tx["ws/a"]; ok {
		t.Fatal("the reader kept its place for an ended session")
	}
}

// The daemon wires the client test to the workspace's stored scope, the ruled
// source (K1a), so the filter is live outside the rig.
func TestWireWatchdogFiltersByTheStoredWorkspaceScope(t *testing.T) {
	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	if err := d.store.CreateWorkspace(&api.Workspace{Name: "clientws", Scope: api.WorkspaceScopeClient}); err != nil {
		t.Fatal(err)
	}
	if err := d.store.CreateWorkspace(&api.Workspace{Name: "ownws"}); err != nil {
		t.Fatal(err)
	}
	w := newWatchdog(d.store, d.events, nil, 0)
	d.wireWatchdog(w)
	if w.client == nil {
		t.Fatal("the daemon left the client test unset")
	}
	if c, err := w.client("clientws"); err != nil || !c {
		t.Errorf("clientws: %v %v, want client", c, err)
	}
	if c, err := w.client("ownws"); err != nil || c {
		t.Errorf("ownws: %v %v, want not client", c, err)
	}
	if _, err := w.client("missing"); err == nil {
		t.Error("an unknown workspace did not report an error")
	}
}
