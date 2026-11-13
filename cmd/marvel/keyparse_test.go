package main

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// arrowRig runs watchLoop on five rows with a fetch that never answers, so the
// view is driven by keys alone, and reports which row is highlighted after
// each frame.
type arrowRig struct {
	t      *testing.T
	keys   chan byte
	frames chan string
	done   chan struct{}
	stop   chan struct{}
}

var seatName = regexp.MustCompile(`seat\d\d`)

func newArrowRig(t *testing.T) *arrowRig {
	t.Helper()
	t.Setenv("MARVEL_SOCKET", "")
	t.Setenv("TMUX", "")
	setWidth(t, 200)
	ws := selRig(t, selRows(5))
	r := &arrowRig{
		t:      t,
		keys:   make(chan byte, 1),
		frames: make(chan string, 256),
		done:   make(chan struct{}),
		stop:   make(chan struct{}),
	}
	cfg := watchLoopConfig{
		interval: time.Second,
		deadline: time.Minute,
		fetch:    func() watchData { <-r.stop; return watchData{} },
		now:      func() time.Time { return watchT0 },
		out: func(frame string) {
			select {
			case r.frames <- frame:
			default:
			}
		},
	}
	go func() { watchLoop(ws, cfg, r.keys, make(chan time.Time)); close(r.done) }()
	t.Cleanup(func() {
		close(r.stop)
		select {
		case r.keys <- 'q':
		case <-time.After(time.Second):
		}
		select {
		case <-r.done:
		case <-time.After(2 * time.Second):
		}
	})
	return r
}

func (r *arrowRig) send(bs ...byte) {
	r.t.Helper()
	for _, b := range bs {
		select {
		case r.keys <- b:
		case <-time.After(2 * time.Second):
			r.t.Fatalf("key %#x was not taken within 2s", b)
		}
	}
}

// sendSlowly sends bytes with gap between them, as a terminal that splits a
// sequence across reads does.
func (r *arrowRig) sendSlowly(gap time.Duration, bs ...byte) {
	r.t.Helper()
	for _, b := range bs {
		r.send(b)
		time.Sleep(gap)
	}
}

// highlighted waits for a frame whose highlighted row is want.
func (r *arrowRig) highlighted(want string) {
	r.t.Helper()
	timeout := time.After(2 * time.Second)
	var last string
	for {
		select {
		case f := <-r.frames:
			for _, l := range regexp.MustCompile(`[^\n]*\x1b\[7m[^\n]*`).FindAllString(f, -1) {
				last = seatName.FindString(l)
			}
			if last == want {
				return
			}
		case <-timeout:
			r.t.Fatalf("highlighted row = %q after 2s, want %q", last, want)
		}
	}
}

const (
	esc       = 0x1b
	pastDelay = 300 * time.Millisecond
	splitGap  = 5 * time.Millisecond
)

// ESC [ B moves the cursor down and ESC [ A moves it up, as j and k do,
// whether the sequence arrives whole, a byte at a time, or with a lone ESC and
// an unknown sequence around it. A lone ESC after the deadline is Escape and
// holds nothing back.
func TestKeyParserArrows(t *testing.T) {
	t.Run("whole", func(t *testing.T) {
		r := newArrowRig(t)
		r.send('j')
		r.highlighted("seat00")
		r.send(esc, '[', 'B')
		r.highlighted("seat01")
		r.send(esc, '[', 'B')
		r.highlighted("seat02")
		r.send(esc, '[', 'A')
		r.highlighted("seat01")
	})
	t.Run("split across reads", func(t *testing.T) {
		r := newArrowRig(t)
		r.send('j')
		r.highlighted("seat00")
		r.sendSlowly(splitGap, esc, '[', 'B')
		r.highlighted("seat01")
		r.sendSlowly(splitGap, esc, '[', 'A')
		r.highlighted("seat00")
	})
	t.Run("lone ESC then a key after the deadline", func(t *testing.T) {
		r := newArrowRig(t)
		r.send('j')
		r.highlighted("seat00")
		r.send(esc)
		time.Sleep(pastDelay)
		r.send('j')
		r.highlighted("seat01")
	})
	t.Run("ESC then a late sequence tail is not an arrow", func(t *testing.T) {
		r := newArrowRig(t)
		r.send('j')
		r.highlighted("seat00")
		r.send(esc)
		time.Sleep(pastDelay)
		r.send('[', 'B') // the deadline passed: these are two plain bytes
		r.send('j')
		r.highlighted("seat01")
	})
	t.Run("ESC then j at once is both keys", func(t *testing.T) {
		r := newArrowRig(t)
		r.send('j')
		r.highlighted("seat00")
		r.send(esc, 'j')
		r.highlighted("seat01")
	})
	t.Run("an unknown sequence moves nothing", func(t *testing.T) {
		r := newArrowRig(t)
		r.send('j')
		r.highlighted("seat00")
		r.send(esc, '[', 'Z')
		r.send('j')
		r.highlighted("seat01")
	})
	t.Run("the loop quits on q after a lone ESC", func(t *testing.T) {
		r := newArrowRig(t)
		r.send(esc)
		time.Sleep(pastDelay)
		r.send('q')
		select {
		case <-r.done:
		case <-time.After(2 * time.Second):
			t.Fatal("the loop did not return after ESC and q")
		}
	})
}

// The multi-cluster watch loop reads keys the same way: an arrow moves its
// cursor.
func TestKeyParserArrowsInTheFanWatch(t *testing.T) {
	r := newFanRig(t, time.Minute, time.Minute, "a")
	r.waitStarted("a", 1)
	r.answer("a", 1, "one", "two", "three")
	r.frame("the answered rows", contains("three"))
	highlightedRow := func(f string) string {
		for _, l := range strings.Split(f, "\n") {
			if strings.Contains(l, "\x1b[7m") {
				return strings.Join(strings.Fields(strings.ReplaceAll(l, "\x1b[7m", " ")), " ")
			}
		}
		return ""
	}
	r.keys <- 'j'
	r.frame("the first row highlighted", func(f string) bool { return strings.Contains(highlightedRow(f), "one") })
	for _, b := range []byte{esc, '[', 'B'} {
		r.keys <- b
	}
	r.frame("the second row (three, by name order) highlighted by ESC [ B", func(f string) bool { return strings.Contains(highlightedRow(f), "three") })
}
