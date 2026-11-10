package codex

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/runtime/events"
)

// One stream line over the 4 MiB cap must not end the parse: the parser reads
// to the newline without keeping the line, reports one bounded parse error, and
// goes on, as the goose parser does. A bufio.Scanner stops at the line and the
// reader above it closes the pipe under a live harness.
func TestParseSurvivesALineOverTheCap(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 4<<20+10)
	stream := long + "\n" + `{"type":"zz"}` + "\n"
	var got []events.Event
	err := NewParser(Config{}).Parse(context.Background(), bytes.NewReader([]byte(stream)), func(ev events.Event) {
		got = append(got, ev)
	})
	if err != nil {
		t.Fatalf("Parse stopped at the long line: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want a parse error then the next line's event: %+v", len(got), got)
	}
	first, ok := got[0].Data.(events.ErrorData)
	if !ok || first.Kind != events.ErrKindParse || !strings.Contains(first.Message, "size limit") {
		t.Fatalf("first event = %+v, want a parse error naming the size limit", got[0])
	}
	if len(first.Message) > 1024 {
		t.Errorf("the parse error is %d bytes: it should quote only a bounded prefix", len(first.Message))
	}
	if got[0].Raw != nil {
		t.Errorf("the over-long line was kept as raw (%d bytes)", len(got[0].Raw))
	}
	second, ok := got[1].Data.(events.ErrorData)
	if !ok || second.Kind != events.ErrKindUnmapped {
		t.Fatalf("second event = %+v, want the next line parsed", got[1])
	}
}
