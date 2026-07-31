package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/runtime/events"
)

// fixtureStream is a two-line stand-in for a claude-code session: init
// then result. The real fixtures live in runtime/claudecode/testdata; the
// instance only needs enough bytes to prove the pipe reaches the parser.
const fixtureStream = `{"type":"system","subtype":"init","session_id":"sess-1","cwd":"/tmp","model":"claude-haiku-4-5"}
{"type":"result","subtype":"success","is_error":false,"session_id":"sess-1","duration_ms":1200,"num_turns":1,"total_cost_usd":0.002,"usage":{"input_tokens":10,"output_tokens":3}}
`

// fakePanes records what a TmuxInstance asked tmux to do and, when
// writeOnSpawn is set, plays a harness that writes to the FIFO.
type fakePanes struct {
	mu           sync.Mutex
	command      string
	env          map[string]string
	killed       []string
	keys         []string
	captured     string
	newPaneErr   error
	writeOnSpawn string // path; empty means the harness writes nothing
	payload      string
	wrote        chan struct{}
}

func (f *fakePanes) NewPane(session, command, title string, envs map[string]string) (string, error) {
	f.mu.Lock()
	f.command = command
	f.env = envs
	f.mu.Unlock()
	if f.newPaneErr != nil {
		return "", f.newPaneErr
	}
	if f.writeOnSpawn != "" {
		go func() {
			w, err := os.OpenFile(f.writeOnSpawn, os.O_WRONLY, 0o600)
			if err != nil {
				return
			}
			_, _ = w.WriteString(f.payload)
			_ = w.Close()
			close(f.wrote)
		}()
	}
	return "%7", nil
}

func (f *fakePanes) KillPane(paneID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.killed = append(f.killed, paneID)
	return nil
}

func (f *fakePanes) SendKeys(paneID, text string, literal, enter bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, text)
	return nil
}

func (f *fakePanes) CapturePane(paneID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.captured, nil
}

func newStreamedInstance(t *testing.T, payload string) (*TmuxInstance, *fakePanes) {
	t.Helper()
	fifo, err := NewFIFO(t.TempDir(), "acme/agent-0")
	if err != nil {
		t.Fatalf("new fifo: %v", err)
	}
	panes := &fakePanes{writeOnSpawn: fifo.Path(), payload: payload, wrote: make(chan struct{})}
	parser, err := NewStreamParser(StreamFormatClaudeCodeJSON, StreamParserConfig{
		AgentID:   "acme/agent-0",
		Workspace: "acme",
	})
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}
	inst := NewTmuxInstance(TmuxConfig{
		Panes:       panes,
		TmuxSession: "marvel-acme",
		Title:       "agent-0",
		Command:     "harness > " + fifo.Path(),
		Stream:      &StreamSource{FIFO: fifo, Parser: parser},
	})
	return inst, panes
}

// drain collects events until the channel closes or the deadline passes.
func drain(t *testing.T, ch <-chan events.Event, timeout time.Duration) []events.Event {
	t.Helper()
	var got []events.Event
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, ev)
		case <-deadline:
			t.Fatalf("timed out after %d events", len(got))
			return got
		}
	}
}

func TestTmuxInstanceStreamsFIFOThroughParser(t *testing.T) {
	inst, panes := newStreamedInstance(t, fixtureStream)

	if err := inst.Spawn(context.Background()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if inst.State() != StateRunning {
		t.Fatalf("state = %s, want running", inst.State())
	}
	if inst.PaneID() != "%7" {
		t.Fatalf("pane id = %q, want %%7", inst.PaneID())
	}

	got := drain(t, inst.Events(), 5*time.Second)
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(got), got)
	}
	if got[0].Event != events.KindSessionStarted {
		t.Errorf("first event = %s, want session.started", got[0].Event)
	}
	if got[0].AgentID != "acme/agent-0" || got[0].Workspace != "acme" {
		t.Errorf("identity not stamped: agent=%q workspace=%q", got[0].AgentID, got[0].Workspace)
	}
	ended, ok := got[1].Data.(events.SessionEndedData)
	if !ok {
		t.Fatalf("second event data = %T, want SessionEndedData", got[1].Data)
	}
	if ended.Metering == nil {
		t.Fatal("expected metering on session.ended")
	}
	if ended.Metering.DurationMS != 1200 || ended.Metering.NumTurns != 1 {
		t.Errorf("metering = %+v, want duration 1200 turns 1", ended.Metering)
	}
	// Sequence numbers must be monotonic across the whole stream.
	if got[0].Seq != 1 || got[1].Seq != 2 {
		t.Errorf("seq = %d,%d — want 1,2", got[0].Seq, got[1].Seq)
	}

	<-panes.wrote
	if err := inst.Kill(context.Background()); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if inst.State() != StateStopped {
		t.Fatalf("state = %s, want stopped", inst.State())
	}
	panes.mu.Lock()
	killed := len(panes.killed)
	panes.mu.Unlock()
	if killed != 1 {
		t.Errorf("killed %d panes, want 1", killed)
	}
}

func TestTmuxInstanceKillReleasesReaderWithNoWriter(t *testing.T) {
	// The failure this guards: a harness that never opens its end of the
	// pipe leaves the reader parked in a blocking open, which no context
	// cancellation can interrupt.
	fifo, err := NewFIFO(t.TempDir(), "acme/never-writes")
	if err != nil {
		t.Fatalf("new fifo: %v", err)
	}
	parser, err := NewStreamParser(StreamFormatClaudeCodeJSON, StreamParserConfig{})
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}
	inst := NewTmuxInstance(TmuxConfig{
		Panes:   &fakePanes{},
		Command: "true",
		Stream:  &StreamSource{FIFO: fifo, Parser: parser},
	})
	if err := inst.Spawn(context.Background()); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- inst.Kill(context.Background()) }()
	select {
	case kerr := <-done:
		if kerr != nil {
			t.Fatalf("kill: %v", kerr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Kill did not return: reader still parked on the pipe")
	}

	if _, statErr := os.Stat(fifo.Path()); !os.IsNotExist(statErr) {
		t.Errorf("fifo survived teardown: %v", statErr)
	}
	if _, ok := <-inst.Events(); ok {
		t.Error("expected a closed event channel after Kill")
	}
}

func TestTmuxInstanceWithoutStreamClosesEventsImmediately(t *testing.T) {
	panes := &fakePanes{captured: "pane text"}
	inst := NewTmuxInstance(TmuxConfig{Panes: panes, Command: "sleep 300", Title: "shell"})

	if err := inst.Spawn(context.Background()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if got := drain(t, inst.Events(), 2*time.Second); len(got) != 0 {
		t.Fatalf("got %d events from a pane-only instance, want 0", len(got))
	}

	if err := inst.Inject("hello"); err != nil {
		t.Fatalf("inject: %v", err)
	}
	out, err := inst.Capture(context.Background())
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if string(out) != "pane text" {
		t.Errorf("capture = %q, want %q", out, "pane text")
	}
	panes.mu.Lock()
	keys := panes.keys
	panes.mu.Unlock()
	if len(keys) != 1 || keys[0] != "hello" {
		t.Errorf("send-keys = %v, want [hello]", keys)
	}
}

func TestTmuxInstanceSecondSpawnRefused(t *testing.T) {
	inst := NewTmuxInstance(TmuxConfig{Panes: &fakePanes{}, Command: "true"})
	if err := inst.Spawn(context.Background()); err != nil {
		t.Fatalf("first spawn: %v", err)
	}
	if err := inst.Spawn(context.Background()); !errors.Is(err, ErrAlreadySpawned) {
		t.Fatalf("second spawn error = %v, want ErrAlreadySpawned", err)
	}
}

func TestTmuxInstanceSpawnFailureRetiresSink(t *testing.T) {
	fifo, err := NewFIFO(t.TempDir(), "acme/doomed")
	if err != nil {
		t.Fatalf("new fifo: %v", err)
	}
	parser, err := NewStreamParser(StreamFormatClaudeCodeJSON, StreamParserConfig{})
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}
	inst := NewTmuxInstance(TmuxConfig{
		Panes:   &fakePanes{newPaneErr: errors.New("tmux said no")},
		Command: "true",
		Stream:  &StreamSource{FIFO: fifo, Parser: parser},
	})
	if err := inst.Spawn(context.Background()); err == nil {
		t.Fatal("expected spawn to fail")
	}
	if inst.State() != StateFailed {
		t.Errorf("state = %s, want failed", inst.State())
	}
	if _, statErr := os.Stat(fifo.Path()); !os.IsNotExist(statErr) {
		t.Errorf("fifo survived a failed spawn: %v", statErr)
	}
}

func TestFIFONameFlattensSessionKey(t *testing.T) {
	dir := t.TempDir()
	fifo, err := NewFIFO(dir, "acme/squad-worker-g1-0")
	if err != nil {
		t.Fatalf("new fifo: %v", err)
	}
	want := filepath.Join(dir, "acme-squad-worker-g1-0.ndjson")
	if fifo.Path() != want {
		t.Errorf("path = %q, want %q", fifo.Path(), want)
	}
	info, err := os.Stat(fifo.Path())
	if err != nil {
		t.Fatalf("stat fifo: %v", err)
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		t.Errorf("mode = %v, want a named pipe", info.Mode())
	}

	// A leftover pipe from a crashed daemon must not block a fresh one.
	if _, err := NewFIFO(dir, "acme/squad-worker-g1-0"); err != nil {
		t.Fatalf("recreate over stale fifo: %v", err)
	}
}

func TestTmuxInstanceDetachLeavesPaneAlone(t *testing.T) {
	// The reap path's shape: the pane is already gone, so Detach must
	// retire the stream without asking tmux to kill anything.
	inst, panes := newStreamedInstance(t, fixtureStream)
	if err := inst.Spawn(context.Background()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	<-panes.wrote
	drain(t, inst.Events(), 5*time.Second)

	inst.Detach(context.Background())
	if inst.State() != StateStopped {
		t.Errorf("state = %s, want stopped", inst.State())
	}
	panes.mu.Lock()
	killed := panes.killed
	panes.mu.Unlock()
	if len(killed) != 0 {
		t.Errorf("Detach killed panes %v", killed)
	}
}
