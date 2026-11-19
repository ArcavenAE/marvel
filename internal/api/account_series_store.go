package api

import "time"

// DefaultPersistRetention is how long a day file of readings is kept on disk.
const DefaultPersistRetention = 90 * 24 * time.Hour

// Attach is not built yet.
func (s *AccountSeries) Attach(dir string, retention time.Duration, now time.Time) error {
	return nil
}
