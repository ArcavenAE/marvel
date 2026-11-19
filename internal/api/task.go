package api

import (
	"unicode"
	"unicode/utf8"
)

// maxTaskLen bounds a task key, in characters. A bd id and an ask id are far
// shorter; the bound keeps a pathological value out of an event and a table.
const maxTaskLen = 128

// ValidTask reports whether s is a usable task key: the id of a bd ticket or of
// an ask, one to maxTaskLen characters, each a graphic character and none a
// space. Control and format characters (a bidi override, a line separator) are
// refused with the spaces, so a key reads the same in a table, a log line and
// an event. It checks the shape only; whether the ticket exists is not asked.
func ValidTask(s string) bool {
	n := utf8.RuneCountInString(s)
	if n == 0 || n > maxTaskLen || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || !unicode.IsGraphic(r) {
			return false
		}
	}
	return true
}

// TaskOf returns the task of the named session, or "" for a session with none
// or no such session. It reads an index kept beside the session map and takes
// no store lock, so the event ring can call it from Emit, which runs inside
// callers that already hold the store's or a controller's lock.
func (s *Store) TaskOf(key string) string {
	if v, ok := s.tasks.Load(key); ok {
		return v.(string)
	}
	return ""
}

// noteTask keeps the index in step with a session being stored or removed.
// A task is set when the session is created and never changes, so the
// create and load paths are the only writers.
func (s *Store) noteTask(key, task string) {
	if task == "" {
		s.tasks.Delete(key)
		return
	}
	s.tasks.Store(key, task)
}
