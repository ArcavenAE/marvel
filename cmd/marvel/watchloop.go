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
func watchLoop(ws *watchSort, cfg watchLoopConfig, keys <-chan byte, ticks <-chan time.Time) {
	draw := func() {
		if !ws.showHelp {
			ws.apply(cfg.fetch())
		}
		cfg.out(renderWatchFrame(ws, cfg.interval, cfg.now()))
	}
	draw()
	for {
		select {
		case key := <-keys:
			quit, redraw := handleWatchKey(ws, key)
			if quit {
				cfg.out("\033[2J\033[H")
				return
			}
			if redraw {
				draw()
			}
		case <-ticks:
			if !ws.showHelp {
				draw()
			}
		}
	}
}
