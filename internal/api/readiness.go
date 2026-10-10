package api

import (
	"errors"
	"time"

	"github.com/arcavenae/marvel/internal/asof"
)

// Stub for the red commit: the shapes the tests compile against, with none of
// the behavior. The next commit replaces it.

type DutyState string

const (
	DutyProven    DutyState = "proven"
	DutyFail      DutyState = "fail"
	DutyUnproven  DutyState = "unproven"
	DutyWithdrawn DutyState = "withdrawn"
)

const SetByDaemon = "daemon"

type DutyRecord struct {
	Seat       string    `json:"seat"`
	Duty       string    `json:"duty"`
	Withdrawn  bool      `json:"withdrawn,omitempty"`
	State      DutyState `json:"state"`
	Source     string    `json:"source,omitempty"`
	Seq        uint64    `json:"seq"`
	ObservedAt time.Time `json:"observed_at"`
	ValidUntil time.Time `json:"valid_until,omitzero"`
	SetBy      string    `json:"set_by"`
}

var ErrWithdrawalNotBuilt = errors.New("withdrawals wait for per-seat caller identity (aae-orc-z8bxv)")

func DutyCellName(duty string) string      { return "duty." + duty }
func WithdrawnCellName(duty string) string { return "withdrawn." + duty }

func ValidateDutyRecord(DutyRecord) error { return nil }

func ReadinessCells([]DutyRecord) map[string]asof.Cell[string] { return nil }

func (s *Store) RecordDuty(r DutyRecord) (DutyRecord, error) { return r, nil }
func (s *Store) ListDuties(string) []DutyRecord              { return nil }
func (s *Store) SeatReadiness(string) map[string]asof.Cell[string] {
	return nil
}
