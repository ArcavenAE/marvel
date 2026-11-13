package main

import "time"

// escWait is how long an ESC waits for the byte that would make it the start
// of an arrow key. A terminal writes the whole sequence at once, so a gap
// this long means the operator pressed Escape. It is a variable so a test can
// pin both sides of the boundary.
var escWait = 50 * time.Millisecond
