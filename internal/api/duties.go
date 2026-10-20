package api

// Duties a role declares (docs/design/seat-readiness.md section 3). Stub for
// the red commit: the library is empty and nothing is validated.

// DutyNames returns the fixed duty library, sorted.
func DutyNames() []string { return nil }

// IsDuty reports whether name is in the library.
func IsDuty(name string) bool { return false }

// validateDuties refuses a duties list a check could not serve.
func validateDuties(where string, duties []string) error { return nil }
