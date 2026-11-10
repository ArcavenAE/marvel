package runtime

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/runtime/events"
)

// faultyParser reads a little of the stream and then fails, as a parser does
// on an unrecoverable fault.
type faultyParser struct{}

func (faultyParser) Parse(_ context.Context, r io.Reader, _ func(events.Event)) error {
	buf := make([]byte, 16)
	if _, err := r.Read(buf); err != nil {
		return err
	}
	return errors.New("parser fault")
}

// A parser fault must not close the pipe under a live harness. The harness's
// next write would then fail (EPIPE), and the seat would go on running with its
// usage and context-pressure signals dark. The reader keeps draining to EOF.
func TestParserFaultDoesNotCloseThePipeUnderTheWriter(t *testing.T) {
	fifo, err := NewFIFO(t.TempDir(), "acme/faulty")
	if err != nil {
		t.Fatalf("new fifo: %v", err)
	}
	inst := NewTmuxInstance(TmuxConfig{
		Panes:   &fakePanes{},
		Command: "true",
		Stream:  &StreamSource{FIFO: fifo, Parser: faultyParser{}},
	})
	if err := inst.Spawn(context.Background()); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	t.Cleanup(func() { _ = inst.Kill(context.Background()) })

	w, err := os.OpenFile(fifo.Path(), os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open the write end: %v", err)
	}
	defer func() { _ = w.Close() }()

	chunk := make([]byte, 64*1024)
	done := make(chan error, 1)
	go func() {
		// Several pipe buffers, so a reader that has gone away is felt.
		for i := 0; i < 32; i++ {
			if _, err := w.Write(chunk); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the harness's write failed after the parser fault: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the harness's write blocked: nothing is draining the pipe")
	}
}
