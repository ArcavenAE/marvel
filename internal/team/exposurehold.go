package team

import (
	"log"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// ExposureSource is where the planner reads what a slot has read. The store
// satisfies it; a test supplies one that fails, to prove the gate fails closed.
type ExposureSource = api.ExposureReader

// exposureIntent is the exposure-gate half of a RolePlan: what applyRolePlan
// should do to the role's latch. The zero value leaves it alone.
type exposureIntent int

const (
	exposureNoop exposureIntent = iota
	exposureClear
	exposureHold
)

// exposureVerdict asks the one gate, api.ExposureVerdict, with the controller's
// source: the injected one in a test, the store otherwise. Caller holds c.mu.
func (c *Controller) exposureVerdict(t *api.Team, role *api.Role) (held bool, reason string) {
	var src ExposureSource = c.store
	if c.Exposures != nil {
		src = c.Exposures
	}
	return api.ExposureVerdict(src, t.Workspace, t.Name, role)
}

// latchExposureHold records that a role's spawn is withheld for an exposure and
// says so once. The same shape as latchBusHold: one event per transition, and a
// standing hold stays quiet on later ticks. Caller holds c.mu.
func (c *Controller) latchExposureHold(t *api.Team, role, reason string) {
	roleKey := t.Workspace + "/" + t.Name + "/" + role
	if c.exposureHolds[roleKey] {
		return
	}
	c.exposureHolds[roleKey] = true
	log.Printf("exposure: %s role %s held: %s", t.Key(), role, reason)
	events.Emit(c.Events, events.Event{
		Kind:      events.KindExposureRefused,
		Severity:  events.SeverityWarning,
		Workspace: t.Workspace,
		Team:      t.Name,
		Role:      role,
		Message:   reason,
	})
}

// clearExposureHold drops the latch when the gate admits again. Quiet, like the
// bus latch. Caller holds c.mu.
func (c *Controller) clearExposureHold(t *api.Team, role string) {
	roleKey := t.Workspace + "/" + t.Name + "/" + role
	if !c.exposureHolds[roleKey] {
		return
	}
	delete(c.exposureHolds, roleKey)
	log.Printf("exposure: %s role %s released", t.Key(), role)
}
