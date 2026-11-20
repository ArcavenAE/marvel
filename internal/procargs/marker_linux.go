//go:build linux

package procargs

import (
	"fmt"
	"os"
)

// ReadMarker reads a process's marvel marker from /proc. The environment is
// read into memory and reduced to the Marker before it returns.
func ReadMarker(pid int) (Marker, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return Marker{}, err
	}
	return ParseEnviron(b), nil
}
