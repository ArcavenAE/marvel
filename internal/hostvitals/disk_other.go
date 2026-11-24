//go:build !darwin && !linux

package hostvitals

import "errors"

// StatVolume is not available on this platform.
func StatVolume(string) (total, free uint64, err error) {
	return 0, 0, errors.New("disk free is not read on this platform")
}
