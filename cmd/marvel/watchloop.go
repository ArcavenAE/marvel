package main

import "time"

// watchFetchDeadline is how long a fetch may run before the watch view says
// the daemon is not answering. A remote connection cannot take a deadline of
// its own, so the bound lives in the view.
const watchFetchDeadline = 5 * time.Second

// watchLoopConfig is what the watch loop needs from outside, so the loop runs
// without a terminal or a daemon.
type watchLoopConfig struct {
	interval time.Duration
	deadline time.Duration
	fetch    func() watchData
	now      func() time.Time
	out      func(frame string)
}

// handleWatchKey applies one key to the view. quit is true for q and Ctrl-C;
// redraw is false for a key the view does not use.
func handleWatchKey(ws *watchSort, key byte) (quit, redraw bool) {
	switch key {
	case 'q', 3: // q or Ctrl-C
		return true, false
	case 'c':
		toggleSort(ws, "context", true)
	case 'p':
		toggleSort(ws, "cpu", true)
	case 'm':
		toggleSort(ws, "rss", true)
	case 'n':
		toggleSort(ws, "name", false)
	case 'r':
		toggleSort(ws, "runtime", false)
	case 'R':
		toggleSort(ws, "role", false)
	case 'l':
		toggleSort(ws, "llm", false)
	case 'g':
		toggleSort(ws, "generation", false)
	case 't':
		toggleSort(ws, "team", false)
	case 'w':
		toggleSort(ws, "workspace", false)
	case 's':
		toggleSort(ws, "state", false)
	case 'd':
		toggleSort(ws, "desk", false)
	case 'h':
		toggleSort(ws, "health", false)
	case 'o':
		toggleSort(ws, "rate", true)
	case '?':
		ws.showHelp = !ws.showHelp
	default:
		return false, false
	}
	return false, true
}

// watchLoop draws the watch view until q or Ctrl-C.
//
// A fetch runs in its own goroutine and the loop never waits on it, so a
// daemon that is not answering cannot take the keys away. At most one fetch
// is outstanding: ticks while it is in flight start nothing, so a dead daemon
// does not pile up connections. If a fetch is still running when its deadline
// passes, the frame keeps the last good data under a stale banner, and the
// banner clears when the fetch answers.
func watchLoop(ws *watchSort, cfg watchLoopConfig, keys <-chan byte, ticks <-chan time.Time) {
	var (
		results  = make(chan watchData, 1)
		inflight bool
		deadline *time.Timer
		late     <-chan time.Time
	)
	start := func() {
		if inflight {
			return
		}
		inflight = true
		deadline = time.NewTimer(cfg.deadline)
		late = deadline.C
		go func() { results <- cfg.fetch() }()
	}
	stopDeadline := func() {
		if deadline != nil {
			deadline.Stop()
		}
		late = nil
	}
	draw := func() { cfg.out(renderWatchFrame(ws, cfg.interval, cfg.now())) }

	start()
	draw()
	for {
		select {
		case key := <-keys:
			quit, redraw := handleWatchKey(ws, key)
			if quit {
				stopDeadline()
				cfg.out("\033[2J\033[H")
				return
			}
			if redraw {
				draw() // from what is on hand; a key never waits on the daemon
			}
		case <-ticks:
			if !ws.showHelp {
				start()
			}
		case d := <-results:
			inflight = false
			stopDeadline()
			ws.apply(d)
			if !ws.showHelp {
				draw()
			}
		case <-late:
			late = nil
			ws.late = true
			if !ws.showHelp {
				draw()
			}
		}
	}
}
