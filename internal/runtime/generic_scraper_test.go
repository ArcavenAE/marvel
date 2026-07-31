package runtime

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeRangeCapturer replays a scripted sequence of capture-pane outputs
// and records the (start, end) arguments each Scrape passed, so a test
// can assert the history form is used and the mark advances.
type fakeRangeCapturer struct {
	outputs []string
	err     error
	calls   [][2]int
	idx     int
}

func (f *fakeRangeCapturer) CapturePaneRange(_ string, start, end int) (string, error) {
	f.calls = append(f.calls, [2]int{start, end})
	if f.err != nil {
		return "", f.err
	}
	out := f.outputs[len(f.outputs)-1]
	if f.idx < len(f.outputs) {
		out = f.outputs[f.idx]
	}
	f.idx++
	return out, nil
}

func TestGenericScraper_UsesHistoryRangeForm(t *testing.T) {
	t.Parallel()
	cap := &fakeRangeCapturer{outputs: []string{"a\nb\n"}}
	s := NewGenericScraper(cap, "%1")

	if _, err := s.Scrape(); err != nil {
		t.Fatalf("scrape: %v", err)
	}
	if len(cap.calls) != 1 {
		t.Fatalf("expected 1 CapturePaneRange call, got %d", len(cap.calls))
	}
	// A history scrape reaches into scrollback (negative start) rather than
	// the visible region — the whole point of the range form.
	if start := cap.calls[0][0]; start >= 0 {
		t.Errorf("start = %d, want a negative scrollback offset", start)
	}
}

func TestGenericScraper_AdvancesHighWaterMark(t *testing.T) {
	t.Parallel()
	// Each poll sees the pane's scrollback grow. Scrape must return only
	// the lines added since the previous poll, never re-emit.
	cap := &fakeRangeCapturer{outputs: []string{
		"line1\nline2\n",
		"line1\nline2\nline3\n",
		"line1\nline2\nline3\nline4\nline5\n",
		"line1\nline2\nline3\nline4\nline5\n", // idle poll, nothing new
	}}
	s := NewGenericScraper(cap, "%1")

	steps := []struct {
		want []string
	}{
		{[]string{"line1", "line2"}},
		{[]string{"line3"}},
		{[]string{"line4", "line5"}},
		{nil},
	}
	for i, step := range steps {
		got, err := s.Scrape()
		if err != nil {
			t.Fatalf("poll %d: %v", i, err)
		}
		if fmt.Sprint(got) != fmt.Sprint(step.want) {
			t.Errorf("poll %d: got %v, want %v", i, got, step.want)
		}
	}
	if s.delivered != 5 {
		t.Errorf("high-water mark = %d, want 5", s.delivered)
	}
}

func TestGenericScraper_TrimsTrailingBlankRows(t *testing.T) {
	t.Parallel()
	// tmux pads a short pane with empty visible rows. Those must not count
	// toward the mark, or real output filling them later is misreported.
	cap := &fakeRangeCapturer{outputs: []string{
		"only line\n\n\n\n",
		"only line\nnext line\n\n\n",
	}}
	s := NewGenericScraper(cap, "%1")

	first, err := s.Scrape()
	if err != nil {
		t.Fatalf("first scrape: %v", err)
	}
	if fmt.Sprint(first) != fmt.Sprint([]string{"only line"}) {
		t.Errorf("first = %v, want [only line]", first)
	}
	second, err := s.Scrape()
	if err != nil {
		t.Fatalf("second scrape: %v", err)
	}
	if fmt.Sprint(second) != fmt.Sprint([]string{"next line"}) {
		t.Errorf("second = %v, want [next line]", second)
	}
}

func TestGenericScraper_RebaselinesOnShrink(t *testing.T) {
	t.Parallel()
	// A screen clear shrinks the buffer. The scraper must rebaseline to the
	// new extent instead of re-emitting or panicking on a slice bound.
	cap := &fakeRangeCapturer{outputs: []string{
		"a\nb\nc\n",
		"x\n", // cleared, fewer lines than delivered
		"x\ny\n",
	}}
	s := NewGenericScraper(cap, "%1")

	if _, err := s.Scrape(); err != nil {
		t.Fatalf("first: %v", err)
	}
	shrunk, err := s.Scrape()
	if err != nil {
		t.Fatalf("shrink poll: %v", err)
	}
	if len(shrunk) != 0 {
		t.Errorf("shrink poll emitted %v, want nothing (rebaseline)", shrunk)
	}
	if s.delivered != 1 {
		t.Errorf("mark after shrink = %d, want 1", s.delivered)
	}
	grown, err := s.Scrape()
	if err != nil {
		t.Fatalf("grow poll: %v", err)
	}
	if fmt.Sprint(grown) != fmt.Sprint([]string{"y"}) {
		t.Errorf("grow poll = %v, want [y]", grown)
	}
}

func TestGenericScraper_PropagatesError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("no pane")
	cap := &fakeRangeCapturer{outputs: []string{""}, err: sentinel}
	s := NewGenericScraper(cap, "%1")

	_, err := s.Scrape()
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want it to wrap %v", err, sentinel)
	}
	if !strings.Contains(err.Error(), "%1") {
		t.Errorf("error %q should name the pane", err)
	}
}
