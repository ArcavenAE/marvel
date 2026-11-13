package main

import "time"

// escWait is how long an ESC waits for the byte that would make it the start
// of an arrow key. A terminal writes the whole sequence at once, so a gap
// this long means the operator pressed Escape.
var escWait = 50 * time.Millisecond

const keyESC = 0x1b

// parseKeys turns terminal arrow sequences in a key stream into the keys the
// view already knows: ESC [ A (up) becomes k and ESC [ B (down) becomes j.
// Every other byte passes through in order. A lone ESC, or an ESC followed by
// anything but '[', is passed on after at most wait, so a key never waits on
// bytes that are not coming. A longer sequence (modified arrows, function
// keys) is dropped whole rather than leaking its tail as keys. The goroutine
// ends when done closes.
func parseKeys(in <-chan byte, wait time.Duration, done <-chan struct{}) <-chan byte {
	out := make(chan byte, 1)
	go func() {
		send := func(bs ...byte) bool {
			for _, b := range bs {
				select {
				case out <- b:
				case <-done:
					return false
				}
			}
			return true
		}
		// next reads one byte within wait; ok is false on a timeout.
		next := func() (b byte, ok, alive bool) {
			t := time.NewTimer(wait)
			defer t.Stop()
			select {
			case b, open := <-in:
				return b, open, open
			case <-t.C:
				return 0, false, true
			case <-done:
				return 0, false, false
			}
		}
		for {
			var b byte
			select {
			case v, open := <-in:
				if !open {
					return
				}
				b = v
			case <-done:
				return
			}
			if b != keyESC {
				if !send(b) {
					return
				}
				continue
			}
			b2, ok, alive := next()
			if !alive {
				return
			}
			if !ok || b2 != '[' {
				if !send(keyESC) {
					return
				}
				if ok && !send(b2) {
					return
				}
				continue
			}
			// ESC [ parameters... final, the final byte in 0x40..0x7e.
			params := 0
			for {
				c, ok, alive := next()
				if !alive {
					return
				}
				if !ok { // the sequence stopped short: hand its bytes on
					if !send(keyESC, '[') {
						return
					}
					break
				}
				if c >= 0x40 && c <= 0x7e {
					if params == 0 {
						switch c {
						case 'A':
							if !send('k') {
								return
							}
						case 'B':
							if !send('j') {
								return
							}
						}
					}
					break
				}
				params++
			}
		}
	}()
	return out
}
