//go:build !darwin && !linux

package procargs

import "errors"

// ReadMarker is not available on this platform.
func ReadMarker(int) (Marker, error) {
	return Marker{}, errors.New("procargs: reading a process marker is not supported on this platform")
}
