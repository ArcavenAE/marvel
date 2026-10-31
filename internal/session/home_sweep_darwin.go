//go:build darwin

package session

import "fmt"

// recordedStart renders the start time codex wrote in a daemon pid file in the
// form childproof reads from the kernel on darwin (p_starttime).
func recordedStart(sec, usec int64) string {
	if sec <= 0 || usec < 0 || usec > 999999 {
		return ""
	}
	return fmt.Sprintf("darwin:%d.%06d", sec, usec)
}
