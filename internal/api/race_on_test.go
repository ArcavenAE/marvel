//go:build race

package api

// raceEnabled is true when the test binary is built with the race detector,
// whose instrumentation changes allocation counts.
const raceEnabled = true
