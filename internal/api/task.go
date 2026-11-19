package api

// ValidTask reports whether s is a usable task key. It is a stub until the
// check is built.
func ValidTask(s string) bool { return false }

// TaskOf returns the task of the named session without taking the store lock.
// It is a stub until the index is built.
func (s *Store) TaskOf(key string) string { return "" }
