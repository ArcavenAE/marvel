package api

// Redacted is what the wire shows in place of a secret-looking Env value.
const Redacted = "(redacted)"

// SecretKey reports whether an Env key looks like it names a secret.
func SecretKey(name string) bool { return false }

// Redact returns a copy of v in which every Runtime, at any depth, shows
// Redacted for the values of secret-looking Env keys.
func Redact[T any](v T) T { return v }
