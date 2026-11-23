package api

import "time"

// AccountLogin is one login's standing as `marvel get account-logins` shows it
// (docs/design/native-checks.md section 2.5). It carries the account key, the
// state, the expiry and the hours left, and never an email or organization.
type AccountLogin struct {
	Account   string     `json:"account"`
	State     string     `json:"state"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// HoursLeft is whole hours to the expiry, omitted when there is no expiry
	// or it has passed.
	HoursLeft *int      `json:"hours_left,omitempty"`
	ReadAt    time.Time `json:"read_at"`
	// Why is the fixed reason of the last failed read.
	Why string `json:"why,omitempty"`
}

// AccountLogins is the answer to get account-logins. Enabled says whether the
// daemon runs the service, so "off" and "no logins" are not the same line.
type AccountLogins struct {
	Enabled bool           `json:"enabled"`
	Logins  []AccountLogin `json:"logins"`
}
