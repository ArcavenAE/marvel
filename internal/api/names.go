package api

import (
	"fmt"
	"regexp"
)

// nameClass is the address class for a workspace, team, role, endpoint or
// policy name: lower-case letters, digits and dashes, starting with a letter or
// digit, at most 32 bytes. It is the class the bus already requires of an
// address segment (internal/bus subjectToken), applied where the name is
// accepted, so a name that reaches a file path, a subject or a format scan
// carries no separator, dot, space or verb.
var nameClass = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// ValidateName reports whether name is in the address class. kind names the
// field in the error.
func ValidateName(kind, name string) error {
	if nameClass.MatchString(name) {
		return nil
	}
	return fmt.Errorf("%s name %q is not addressable: use lower-case letters, digits and dashes, starting with a letter or digit, at most 32 bytes", kind, name)
}
