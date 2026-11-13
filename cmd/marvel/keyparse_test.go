package main

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// arrowDeadline is the escape deadline the tests pin. splitGap sits well
// inside it and pastDelay well beyond it, so both sides of the boundary are
// tested on purpose.
const (
	esc           = 0x1b
	arrowDeadline = 50 * time.Millisecond
	splitGap      = arrowDeadline / 20
	pastDelay     = 4 * arrowDeadline
	quietFor      = 150 * time.Millisecond
)

// pinDeadline sets the parser's escape deadline for one test.
func pinDeadline(t *testing.T) {
	t.Helper()
	old := escWait
	escWait = arrowDeadline
	t.Cleanup(func() { escWait = old })
}

// rowWatcher follows which row a stream of frames highlights.
type rowWatcher struct {
	t       *testing.T
	frames  <-chan string
	extract func(frame string) string
	cur     string
}

// settle waits until the highlighted row is want and no further frame arrives
// for quietFor, so a wrong row shown on the way, or one that appears after the
// right one, fails it.
func (w *rowWatcher) settle(want string) {
	w.t.Helper()
	overall := time.After(2 * time.Second)
	quiet := time.NewTimer(quietFor)
	defer quiet.Stop()
	for {
		select {
		case f := <-w.frames:
			if row := w.extract(f); row != "" {
				w.cur = row
			}
			if !quiet.Stop() {
				select {
				case <-quiet.C:
				default:
				}
			}
			quiet.Reset(quietFor)
		case <-quiet.C:
			if w.cur == want {
				return
			}
			quiet.Reset(quietFor)
		case <-overall:
			w.t.Fatalf("highlighted row = %q after 2s, want %q settled", w.cur, want)
		}
	}
}

// arrowRig runs watchLoop on five rows with a fetch that never answers, so the
// view is driven by keys alone.
type arrowRig struct {
	*rowWatcher
	keys chan byte
	done chan struct{}
	stop chan struct{}
}

var (
	seatName   = regexp.MustCompile(`seat\d\d`)
	highlitRow = regexp.MustCompile(`[^\n]*\x1b\[7m[^\n]*`)
)

func newArrowRig(t *testing.T) *arrowRig {
	t.Helper()
	pinDeadline(t)
	t.Setenv("MARVEL_SOCKET", "")
	t.Setenv("TMUX", "")
	setWidth(t, 200)
	ws := selRig(t, selRows(5))
	frames := make(chan string, 256)
	r := &arrowRig{
		keys: make(chan byte, 1),
		done: make(chan struct{}),
		stop: make(chan struct{}),
	}
	r.rowWatcher = &rowWatcher{t: t, frames: frames, extract: func(f string) string {
		var last string
		for _, l := range highlitRow.FindAllString(f, -1) {
			last = seatName.FindString(l)
		}
		return last
	}}
	cfg := watchLoopConfig{
		interval: time.Second,
		deadline: time.Minute,
		fetch:    func() watchData { <-r.stop; return watchData{} },
		now:      func() time.Time { return watchT0 },
		out: func(frame string) {
			select {
			case frames <- frame:
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
	sendKeys(r.t, r.keys, bs...)
}

func sendKeys(t *testing.T, keys chan<- byte, bs ...byte) {
	t.Helper()
	for _, b := range bs {
		select {
		case keys <- b:
		case <-time.After(2 * time.Second):
			t.Fatalf("key %#x was not taken within 2s", b)
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

// ESC [ B moves the cursor down and ESC [ A moves it up, as j and k do,
// whether the sequence arrives whole or a byte at a time. A lone ESC after the
// deadline is Escape and holds nothing back, and a sequence tail that arrives
// after the deadline is plain bytes, not an arrow.
func TestKeyParserArrows(t *testing.T) {
	t.Run("whole", func(t *testing.T) {
		r := newArrowRig(t)
		r.send('j')
		r.settle("seat00")
		r.send(esc, '[', 'B')
		r.settle("seat01")
		r.send(esc, '[', 'B')
		r.settle("seat02")
		r.send(esc, '[', 'A')
		r.settle("seat01")
	})
	t.Run("split across reads", func(t *testing.T) {
		r := newArrowRig(t)
		r.send('j')
		r.settle("seat00")
		r.sendSlowly(splitGap, esc, '[', 'B')
		r.settle("seat01")
		r.sendSlowly(splitGap, esc, '[', 'A')
		r.settle("seat00")
	})
	t.Run("lone ESC then a key after the deadline", func(t *testing.T) {
		r := newArrowRig(t)
		r.send('j')
		r.settle("seat00")
		r.send(esc)
		time.Sleep(pastDelay)
		r.send('j')
		r.settle("seat01")
	})
	t.Run("a sequence tail after the deadline is not an arrow", func(t *testing.T) {
		r := newArrowRig(t)
		r.send('j')
		r.settle("seat00")
		r.send(esc)
		time.Sleep(pastDelay)
		r.send('[', 'B')
		// Two plain bytes, neither a key. This settle must stay paired with the
		// move below: the move is what catches a wrong frame that arrives late.
		r.settle("seat00")
		r.send('j')
		r.settle("seat01")
	})
	t.Run("ESC then j at once is both keys", func(t *testing.T) {
		r := newArrowRig(t)
		r.send('j')
		r.settle("seat00")
		r.send(esc, 'j')
		r.settle("seat01")
	})
	t.Run("an unknown sequence moves nothing", func(t *testing.T) {
		r := newArrowRig(t)
		r.send('j')
		r.settle("seat00")
		r.send(esc, '[', 'Z')
		// As above: keep this paired with the move below, which catches a late
		// wrong frame.
		r.settle("seat00")
		r.send('j')
		r.settle("seat01")
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
	pinDeadline(t)
	r := newFanRig(t, time.Minute, time.Minute, "a")
	r.waitStarted("a", 1)
	r.answer("a", 1, "one", "two", "three")
	r.frame("the answered rows", contains("three"))
	w := &rowWatcher{t: t, frames: r.frames, extract: func(f string) string {
		for _, l := range strings.Split(f, "\n") {
			if !strings.Contains(l, "\x1b[7m") {
				continue
			}
			fields := strings.Fields(strings.ReplaceAll(l, "\x1b[7m", " "))
			for _, name := range []string{"one", "two", "three"} {
				if slices.Contains(fields, name) {
					return name
				}
			}
		}
		return ""
	}}
	sendKeys(t, r.keys, 'j')
	w.settle("one")
	sendKeys(t, r.keys, esc, '[', 'B')
	w.settle("three") // rows sort by name: one, three, two
	sendKeys(t, r.keys, esc, '[', 'A')
	w.settle("one")
}
