package api

import "fmt"

// Placement is where a session's harness runs: on the host's file system or
// under a container runtime (docs/design/workspace-sandbox-shape.md, section 3).
// The empty value means the workspace named none.
type Placement string

const (
	PlacementLocalhost Placement = "localhost"
	PlacementContainer Placement = "container"
)

// Valid reports whether p is one of the two placements marvel knows.
func (p Placement) Valid() bool {
	return p == PlacementLocalhost || p == PlacementContainer
}

func (p Placement) check(what string) error {
	if p == "" || p.Valid() {
		return nil
	}
	return fmt.Errorf("%s %q is not one of %q, %q", what, string(p), string(PlacementLocalhost), string(PlacementContainer))
}

// ResolvePlacement applies the resolution order of section 5 (ruling D2): the
// workspace's explicit value first, then the harness's recommendation, then
// localhost. An explicit localhost always runs on the host, whatever the harness
// recommends. An unknown value in either slot is an error and never falls
// through to a lower rung, so a stored typo cannot launch somewhere other than
// where it was written.
//
// It decides the value only. What a launch does when the resolved placement
// cannot be had is not part of this function.
func ResolvePlacement(explicit, recommended Placement) (Placement, error) {
	if err := explicit.check("workspace.placement"); err != nil {
		return "", err
	}
	if err := recommended.check("recommended placement"); err != nil {
		return "", err
	}
	switch {
	case explicit != "":
		return explicit, nil
	case recommended != "":
		return recommended, nil
	default:
		return PlacementLocalhost, nil
	}
}
