//go:build !linux && !darwin

package procargs

import "errors"

// Cwd has no source on this system.
func Cwd(int) (string, error) {
	return "", errors.New("no working directory source on this system")
}
