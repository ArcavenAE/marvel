//go:build !darwin

package session

// networkMounts has nothing to list off darwin: the deny is a darwin guard.
func networkMounts() []string { return nil }
