//go:build !race

package api

// raceEnabled is false when the test binary is built without the race detector.
const raceEnabled = false
