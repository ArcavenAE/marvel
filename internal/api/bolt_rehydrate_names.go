package api

import (
	"fmt"
	"log"
)

// A row stored before the name rule can carry a name that reaches a format
// scan, a file path or a subject. Rehydrate applies the rule to what it reads.
// A declared workspace, team or endpoint row is left out with one logged line
// and a record on the Store, because the next apply writes it again. A policy
// row refuses the start: leaving one out can drop a restriction and widen
// access without a sign. A session row refuses the start too: dropping it would
// leave a live seat untracked, and the reconciler could start a second one. A
// session is judged on its workspace, team and role fields, not on its own
// name, which is the role plus an ordinal the controller builds.

// nameField is one name a row carries, with the label the error uses.
type nameField struct{ kind, name string }

// setFields drops the fields that hold no name at all.
func setFields(fields ...nameField) []nameField {
	out := fields[:0:0]
	for _, f := range fields {
		if f.name != "" {
			out = append(out, f)
		}
	}
	return out
}

// rehydrateNameError reports the first field of a stored row that is outside
// the name class, naming the bucket and the key so the row can be found.
func rehydrateNameError(bucket, key string, fields ...nameField) error {
	for _, f := range fields {
		if err := ValidateName(f.kind, f.name); err != nil {
			return fmt.Errorf("stored row in bucket %s, key %q: %w", bucket, key, err)
		}
	}
	return nil
}

// skipRehydrated records one left-out row.
func (s *Store) skipRehydrated(err error) {
	log.Printf("rehydrate: leaving out %v", err)
	s.rehydrateSkipped = append(s.rehydrateSkipped, err.Error())
}

// RehydrateSkipped returns one line for each stored row that daemon start left
// out under the name rule.
func (s *Store) RehydrateSkipped() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.rehydrateSkipped...)
}
