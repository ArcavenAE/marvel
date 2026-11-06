package api

import (
	"fmt"
	"slices"
	"strings"
)

// AmbientBearerVar pairs an environment variable name with the kind word the
// record and the SERVICE cell use for it, in the fixed order the record joins
// them.
type AmbientBearerVar struct{ Env, Kind string }

// AmbientBearerVars are the variables whose presence in the tmux environment is
// recorded. A name is the whole fact: no value is ever read into the record.
var AmbientBearerVars = []AmbientBearerVar{
	{"ANTHROPIC_API_KEY", "api-key"},
	{"ANTHROPIC_AUTH_TOKEN", "auth-token"},
	{"CLAUDE_CODE_OAUTH_TOKEN", "oauth-token"},
}

// Closed-set words of the AmbientBearer record besides the kinds.
const (
	AmbientBearerNone    = "none"
	AmbientBearerUnknown = "unknown"
)

// ValidAmbientBearer reports whether v is a value the record may hold.
func ValidAmbientBearer(v string) bool {
	switch v {
	case "", AmbientBearerNone, AmbientBearerUnknown:
		return true
	}
	last := -1
	for _, kind := range strings.Split(v, "+") {
		i := slices.IndexFunc(AmbientBearerVars, func(b AmbientBearerVar) bool { return b.Kind == kind })
		if i < 0 || i <= last {
			return false
		}
		last = i
	}
	return true
}

// SetAmbientBearer stores v, or refuses it and leaves the record as it was.
// It is the only writer on the spawn path. The store copies a checked record
// whole and JSON decode also writes the field, so the cell treats a stored value
// outside the set as unknown; a free string never prints.
func (s *Session) SetAmbientBearer(v string) error {
	if !ValidAmbientBearer(v) {
		return fmt.Errorf("ambient bearer record: value is outside the closed set")
	}
	s.AmbientBearer = v
	return nil
}
