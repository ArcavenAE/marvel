//go:build !linux && !darwin

package procargs

import "errors"

// OwnerUID has no source on this system.
func OwnerUID(int) (int, error) { return 0, errors.New("no owner source on this system") }
