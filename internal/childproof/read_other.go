//go:build !linux && !darwin

package childproof

import "errors"

// readIdentity has no source on this system. Nothing is ever proven, so the
// broker is never adopted or signalled; marvel spawns afresh when the port is
// free and refuses when it is held. That is fail closed.
func readIdentity(int) (Identity, error) {
	return Identity{}, errors.New("childproof: no process identity source on this system")
}
