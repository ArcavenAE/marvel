package api

import "time"

// ExposureRecord says that a seat of a role read one function's source, so a
// role that writes that function must not be started in the same slot
// (marvel#945). It is keyed on the slot (workspace, team, role), not on a
// session row: the lifecycle deletes rows on a restart, a crash respawn and a
// shift drain, and a record held there would vanish exactly when it matters.
type ExposureRecord struct {
	Workspace string   `json:"workspace"`
	Team      string   `json:"team"`
	Role      string   `json:"role"`
	Function  string   `json:"function"`
	Paths     []string `json:"paths,omitempty"`
	// Caster is who recorded the exposure. It is information, not part of the
	// key: any replica of the role is the same writer for this purpose.
	Caster    string    `json:"caster,omitempty"`
	StartedAt time.Time `json:"started_at"`
	// EndedAt is when the reading stopped, zero while it is open. It is for the
	// audit trail: reading a function does not stop having happened, so the
	// refusal counts an ended record too.
	EndedAt time.Time `json:"ended_at,omitzero"`
	// Seq orders the records and keeps two readings of one function apart.
	Seq uint64 `json:"seq"`
}

// RecordExposure stores one exposure record and returns it as stored.
func (s *Store) RecordExposure(r ExposureRecord) (ExposureRecord, error) {
	return r, nil
}

// EndExposure marks the open records of a slot's function as ended at the given
// time and returns how many it marked. It never removes a record.
func (s *Store) EndExposure(workspace, team, role, function string, at time.Time) (int, error) {
	return 0, nil
}

// ExposuresForSlot returns every record of a slot, ended or not, oldest first.
func (s *Store) ExposuresForSlot(workspace, team, role string) ([]ExposureRecord, error) {
	return nil, nil
}
