package team

// ShiftOpts are the operator's choices for one manual shift.
type ShiftOpts struct {
	// NoHandoff shifts without asking the departing seat for the handoff its
	// role declares. It is the operator's explicit decision to shift without a
	// watcher (docs/design/shift-trigger-list.md D5 point 3).
	NoHandoff bool
}

// InitiateShiftOpts starts a manual shift. This is the seam for marvel#1447:
// today it is InitiateShift, and opts changes nothing.
func (c *Controller) InitiateShiftOpts(teamKey, role string, _ ShiftOpts) error {
	return c.InitiateShift(teamKey, role)
}
