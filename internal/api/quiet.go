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

// Quiet is the one quiet test: a session is quiet at now when its WorkAt (the
// last reading change, else ContextAt) is zero or strictly older than window. At
// exactly the window it is not quiet yet. The rate and the stalled advisory call
// it directly; ACTIVE% calls it over the tick ring, which records each tick's
// WorkAt (marvel#801).
func Quiet(s *Session, window time.Duration, now time.Time) bool {
	w := WorkAt(s)
	return w.IsZero() || now.Sub(w) > window
}

// WorkAt is the last time the session showed a sign of work: when its reading
// last changed, or ContextAt for a producer that never wrote that stamp.
func WorkAt(s *Session) time.Time {
	if !s.ReadingChangedAt.IsZero() {
		return s.ReadingChangedAt
	}
	return s.ContextAt
}
