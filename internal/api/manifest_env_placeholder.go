package api

import (
	"fmt"
	"sort"
	"strings"
)

// EnvPlaceholder is the text a tracked copy of a manifest carries where a
// value was redacted. It is never a real value, so a role env that holds it
// exactly is a copy applied raw.
const EnvPlaceholder = "<value>"

// ValidateEnvPlaceholders refuses a manifest whose role env holds the literal
// EnvPlaceholder. Applied as it stands, the literal would reach the seat's
// environment and nothing would say the real value was missing. Every offending
// role and key is named, so one apply shows all of them. A value is refused
// only when it is the placeholder after trimming space; a value that merely
// contains the text is a real value.
func (m *Manifest) ValidateEnvPlaceholders() error {
	var found []string
	for ti, t := range m.Teams {
		for ri, r := range t.Roles {
			keys := make([]string, 0, len(r.Runtime.Env))
			for k, v := range r.Runtime.Env {
				if strings.TrimSpace(v) == EnvPlaceholder {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			for _, k := range keys {
				found = append(found, fmt.Sprintf("  team[%d=%s].role[%d=%s]: env %s", ti, t.Name, ri, r.Name, k))
			}
		}
	}
	if len(found) == 0 {
		return nil
	}
	return fmt.Errorf("env pre-flight failed on %d key(s): the value is the literal %s, a redaction marker from a tracked copy; apply the manifest that holds the real values:\n%s",
		len(found), EnvPlaceholder, strings.Join(found, "\n"))
}
