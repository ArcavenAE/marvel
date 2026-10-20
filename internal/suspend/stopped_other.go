//go:build !linux && !darwin

package suspend

import "errors"

// readStopped has no reader on this platform, so no process ever counts as
// stopped and a Freeze rolls back.
func readStopped(int) (bool, error) {
	return false, errors.New("suspend: no stop-state reader on this platform")
}
