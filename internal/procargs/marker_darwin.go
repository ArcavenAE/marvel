//go:build darwin

package procargs

import "golang.org/x/sys/unix"

// ReadMarker reads a process's marvel marker from the kernel. The whole
// kern.procargs2 buffer is read into memory and reduced to the Marker before it
// returns; nothing else is kept.
func ReadMarker(pid int) (Marker, error) {
	b, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return Marker{}, err
	}
	return ParseProcargs2Marker(b)
}
