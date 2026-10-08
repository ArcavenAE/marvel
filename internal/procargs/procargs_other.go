//go:build !linux && !darwin

package procargs

import "errors"

// Read has no exact source on this system, so the pre-flight
// refuses rather than guess from a flattened line.
func Read(int) ([]string, error) {
	return nil, errors.New("no exact argument source on this system")
}
