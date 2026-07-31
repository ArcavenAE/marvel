package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/arcavenae/marvel/internal/runtime/events"
)

// PaneController is the slice of the tmux driver a TmuxInstance needs.
// Declaring it here keeps package runtime free of a tmux dependency and
// lets tests drive an instance without a tmux server.
type PaneController interface {
	NewPane(session, command, title string, envs map[string]string) (string, error)
	KillPane(paneID string) error
	SendKeys(paneID, text string, literal, enter bool) error
	CapturePane(paneID string) (string, error)
}

// StreamSource pairs the sink an adapter redirected into with the parser
// that reads it. The session manager builds one when the adapter reports
// a StreamSpec; nil means the instance supervises a pane without
// observing it.
type StreamSource struct {
	FIFO   *FIFO
	Parser StreamParser
}

// TmuxConfig is the launch recipe for one TmuxInstance.
type TmuxConfig struct {
	Panes       PaneController
	TmuxSession string
	Title       string
	Command     string
	Env         map[string]string
	Stream      *StreamSource
	// EventBuffer bounds the channel Events returns. A full channel
	// applies back-pressure to the parser, which applies it to the pipe,
	// which applies it to the harness — the chain is intentional.
	EventBuffer int
}

// DefaultEventBuffer holds a burst of tool calls without stalling the
// harness while the daemon's ring consumer catches up.
const DefaultEventBuffer = 256

// TmuxInstance is the Instance implementation for a harness running in a
// tmux pane. It is the seam every session goes through, whether or not
// there is a stream to read: a pane-only session still gets Inject,
// Capture, Kill, and State through one path.
type TmuxInstance struct {
	cfg    TmuxConfig
	out    chan events.Event
	state  atomic.Int32
	closed sync.Once

	mu     sync.Mutex
	paneID string
	cancel context.CancelFunc

	readerG sync.WaitGroup
}

// ErrAlreadySpawned reports a second Spawn on the same instance. Restart
// means a fresh Instance, per the Instance contract.
var ErrAlreadySpawned = errors.New("instance already spawned")

// NewTmuxInstance builds an unspawned instance.
func NewTmuxInstance(cfg TmuxConfig) *TmuxInstance {
	if cfg.EventBuffer <= 0 {
		cfg.EventBuffer = DefaultEventBuffer
	}
	return &TmuxInstance{
		cfg: cfg,
		out: make(chan events.Event, cfg.EventBuffer),
	}
}

// Spawn creates the pane. When the instance has a stream, the reader is
// parked on the pipe first: both ends of a FIFO block until the other
// arrives, and the harness is the end marvel cannot make wait.
func (i *TmuxInstance) Spawn(_ context.Context) error {
	if !i.state.CompareAndSwap(int32(StateIdle), int32(StateSpawning)) {
		return ErrAlreadySpawned
	}
	if i.cfg.Stream != nil {
		i.startReader()
	} else {
		// Nothing will ever be emitted; consumers should not wait for it.
		i.closeEvents()
	}

	paneID, err := i.cfg.Panes.NewPane(i.cfg.TmuxSession, i.cfg.Command, i.cfg.Title, i.cfg.Env)
	if err != nil {
		i.state.Store(int32(StateFailed))
		i.teardownStream()
		return fmt.Errorf("spawn pane in %s: %w", i.cfg.TmuxSession, err)
	}

	i.mu.Lock()
	i.paneID = paneID
	i.mu.Unlock()
	i.state.Store(int32(StateRunning))
	return nil
}

// Kill terminates the pane and retires the stream. Safe to call on an
// instance whose pane is already gone (the reap path does exactly that);
// the pane error is returned but the stream is torn down either way.
func (i *TmuxInstance) Kill(ctx context.Context) error {
	switch State(i.state.Load()) {
	case StateStopped, StateFailed:
		return nil
	}
	i.state.Store(int32(StateStopping))

	var err error
	if paneID := i.PaneID(); paneID != "" {
		if kerr := i.cfg.Panes.KillPane(paneID); kerr != nil {
			err = fmt.Errorf("kill pane %s: %w", paneID, kerr)
		}
	}
	i.teardownStreamCtx(ctx)

	if err != nil {
		i.state.Store(int32(StateFailed))
		return err
	}
	i.state.Store(int32(StateStopped))
	return nil
}

// Detach retires the stream without touching the pane. It is the reap
// path's Kill: the harness is already gone, so asking tmux to kill a pane
// that no longer exists would only produce a misleading error.
func (i *TmuxInstance) Detach(ctx context.Context) {
	switch State(i.state.Load()) {
	case StateStopped, StateFailed:
		return
	}
	i.state.Store(int32(StateStopping))
	i.teardownStreamCtx(ctx)
	i.state.Store(int32(StateStopped))
}

// Inject types input into the pane. Literal keys plus Enter: the harness
// sees what a human at the keyboard would have sent.
func (i *TmuxInstance) Inject(input string) error {
	paneID := i.PaneID()
	if paneID == "" {
		return errors.New("inject: instance has no pane")
	}
	return i.cfg.Panes.SendKeys(paneID, input, true, true)
}

// Capture snapshots visible pane contents. Present for every instance,
// stream or not — it is the fallback observation channel for harnesses
// that emit nothing structured.
func (i *TmuxInstance) Capture(_ context.Context) ([]byte, error) {
	paneID := i.PaneID()
	if paneID == "" {
		return nil, errors.New("capture: instance has no pane")
	}
	out, err := i.cfg.Panes.CapturePane(paneID)
	if err != nil {
		return nil, err
	}
	return []byte(out), nil
}

// Events returns the normalized stream. Closed when the harness's output
// ends, after Kill, or immediately for instances with no stream.
func (i *TmuxInstance) Events() <-chan events.Event { return i.out }

// State reports the lifecycle stage.
func (i *TmuxInstance) State() State { return State(i.state.Load()) }

// PaneID is the tmux pane the instance owns, empty before Spawn.
func (i *TmuxInstance) PaneID() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.paneID
}

// startReader parks a goroutine on the pipe. It owns the event channel:
// it is the only sender, so it is also the closer.
func (i *TmuxInstance) startReader() {
	ctx, cancel := context.WithCancel(context.Background())
	i.mu.Lock()
	i.cancel = cancel
	i.mu.Unlock()
	i.readerG.Add(1)
	go func() {
		defer i.readerG.Done()
		defer i.closeEvents()

		f, err := i.cfg.Stream.FIFO.Open()
		if err != nil {
			i.emitTransportError(ctx, err)
			return
		}
		defer func() { _ = f.Close() }()

		perr := i.cfg.Stream.Parser.Parse(ctx, f, func(ev events.Event) {
			i.emit(ctx, ev)
		})
		if perr != nil && !errors.Is(perr, context.Canceled) && !errors.Is(perr, io.EOF) {
			i.emitTransportError(ctx, perr)
		}
	}()
}

// emit hands an event to the consumer, abandoning it if the instance is
// being torn down. Dropping on cancellation is correct: the alternative
// is a goroutine parked forever on a channel nobody will read again.
func (i *TmuxInstance) emit(ctx context.Context, ev events.Event) {
	select {
	case i.out <- ev:
	case <-ctx.Done():
	}
}

func (i *TmuxInstance) emitTransportError(ctx context.Context, err error) {
	i.emit(ctx, events.Event{
		SchemaVersion: events.SchemaVersion,
		Event:         events.KindError,
		TS:            time.Now().UTC(),
		Data: events.ErrorData{
			Kind:    events.ErrKindTransport,
			Message: err.Error(),
		},
	})
}

func (i *TmuxInstance) closeEvents() {
	i.closed.Do(func() { close(i.out) })
}

// streamTeardownGrace bounds the wait for a reader to notice it is done.
// The timeout keeps a wedged pipe from holding up session deletion.
const streamTeardownGrace = 2 * time.Second

// streamPokeInterval is how often teardown retries the release. One poke
// is not enough: a reader that has not yet reached its blocking open
// cannot be released, and there is no way to observe the moment it parks.
const streamPokeInterval = 20 * time.Millisecond

func (i *TmuxInstance) teardownStream() {
	ctx, cancel := context.WithTimeout(context.Background(), streamTeardownGrace)
	defer cancel()
	i.teardownStreamCtx(ctx)
}

// teardownStreamCtx retires the pipe and waits for the reader to leave.
// It does not close the event channel: the reader is the only sender, so
// the reader is the only safe closer. A wait that times out therefore
// leaves the channel open — the alternative is a panic on a closed
// channel, and a lingering consumer is the cheaper failure.
func (i *TmuxInstance) teardownStreamCtx(ctx context.Context) {
	if i.cfg.Stream == nil {
		return
	}
	i.mu.Lock()
	cancel := i.cancel
	i.mu.Unlock()
	if cancel != nil {
		// Stops the parse loop at its next line boundary.
		cancel()
	}

	done := make(chan struct{})
	go func() {
		i.readerG.Wait()
		close(done)
	}()

	deadline := time.NewTimer(streamTeardownGrace)
	defer deadline.Stop()
	tick := time.NewTicker(streamPokeInterval)
	defer tick.Stop()
	for {
		// Release a reader parked in the blocking open. Cancellation
		// cannot reach it; only a writer arriving can.
		i.cfg.Stream.FIFO.Poke()
		select {
		case <-done:
		case <-ctx.Done():
		case <-deadline.C:
		case <-tick.C:
			continue
		}
		break
	}
	_ = i.cfg.Stream.FIFO.Remove()
}

func init() {
	var _ Instance = (*TmuxInstance)(nil)
}
