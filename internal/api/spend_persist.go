package api

import "time"

// SpendPersistInterval is the longest a session's stream spend goes unwritten
// to the bolt file while updates keep arriving (marvel#777, ruling d1117d-8).
const SpendPersistInterval = 15 * time.Minute

// SetSpendClock replaces the clock the spend persist reads, for tests. A nil fn
// restores time.Now.
func (s *Store) SetSpendClock(fn func() time.Time) {}
