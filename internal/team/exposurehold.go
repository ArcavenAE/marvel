package team

import "github.com/arcavenae/marvel/internal/api"

// ExposureSource is where the planner reads what a slot has read. The store
// satisfies it; a test supplies one that fails, to prove the gate fails closed.
type ExposureSource interface {
	ExposuresForSlot(workspace, team, role string) ([]api.ExposureRecord, error)
}
