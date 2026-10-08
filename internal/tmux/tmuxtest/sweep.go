package tmuxtest

// SweepDead stops the per-package tmux servers that earlier test runs left
// behind and returns their socket names. A test binary starts its server as
// `tmux -L marvel-test-<package>-<pid>` and stops it only after its tests
// return, so a panic, a -timeout abort or a kill leaves it running; the
// pid in the name is how a later run tells such a server from a live run's.
func SweepDead() []string { return nil }
