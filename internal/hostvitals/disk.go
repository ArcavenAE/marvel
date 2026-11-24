//go:build darwin || linux

package hostvitals

import "syscall"

// StatVolume reports the total and available bytes of the volume that holds
// path.
func StatVolume(path string) (total, free uint64, err error) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return 0, 0, err
	}
	bs := uint64(fs.Bsize)                                     //nolint:unconvert // the field widths differ by platform
	return uint64(fs.Blocks) * bs, uint64(fs.Bavail) * bs, nil //nolint:unconvert // the field widths differ by platform
}
