package api

import "time"

// DefaultQuietWindow is how long a session may go without a sign of work
// before marvel reads it as quiet, when neither its role nor the operator
// says otherwise (docs/design/get-sessions-output.md section 4.4). The
// watchdog's default window and the output-rate's validity read this one
// constant, so marvel has one default quiet window, not three tens.
const DefaultQuietWindow = 10 * time.Minute

// QuietWindow is the window W for a session of role: the role's own
// activity_timeout when it declares one, else the cluster's quiet window
// (the operator's watchdog.window; zero when unset), else
// DefaultQuietWindow. A non-positive value is not a window.
func QuietWindow(role *Role, cluster time.Duration) time.Duration {
	switch {
	case role != nil && role.ActivityTimeout > 0:
		return role.ActivityTimeout
	case cluster > 0:
		return cluster
	default:
		return DefaultQuietWindow
	}
}

// Quiet is the one quiet test: a session is quiet at now when its
// ContextAt is zero or strictly older than window. At exactly the window it
// is not quiet yet. The rate, ACTIVE% and the stalled advisory all call it.
func Quiet(s *Session, window time.Duration, now time.Time) bool {
	return s.ContextAt.IsZero() || now.Sub(s.ContextAt) > window
}
