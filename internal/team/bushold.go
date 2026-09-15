package team

import (
	"log"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// busIntent is the bus-gate half of a RolePlan: what applyRolePlan should do
// to the role's bus-hold latch. The zero value leaves it alone, which is what
// a role that never reached the spawn branch wants.
type busIntent int

const (
	busNoop busIntent = iota
	busClear
	busHold
)

// latchBusHold records that a role's spawn is withheld for the bus and says so
// once. The same shape as latchAdmissionHold: one event per transition, the
// standing outage stays quiet on later ticks. Caller holds c.mu.
func (c *Controller) latchBusHold(t *api.Team, role, reason string) {
	roleKey := t.Workspace + "/" + t.Name + "/" + role
	if c.busHolds[roleKey] {
		return
	}
	c.busHolds[roleKey] = true
	log.Printf("bus: %s role %s held: %s", t.Key(), role, reason)
	events.Emit(c.Events, events.Event{
		Kind:      events.KindBusUnavailable,
		Severity:  events.SeverityWarning,
		Workspace: t.Workspace,
		Team:      t.Name,
		Role:      role,
		Message:   reason,
	})
}

// clearBusHold drops the latch when the gate admits again. Quiet: the bus
// emits bus.started when it is back, so a per-role release event would only
// repeat that N times. Caller holds c.mu.
func (c *Controller) clearBusHold(t *api.Team, role string) {
	roleKey := t.Workspace + "/" + t.Name + "/" + role
	if !c.busHolds[roleKey] {
		return
	}
	delete(c.busHolds, roleKey)
	log.Printf("bus: %s role %s released", t.Key(), role)
}
