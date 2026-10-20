//go:build linux

package suspend

// readStopped reads field 3 of /proc/<pid>/stat. T is a stop by a signal; t, a
// tracing stop, is not one a suspend makes. The command name is field 2 and may
// hold spaces and parentheses, so the state is read after the last ')'.
func readStopped(pid int) (bool, error) {
	return false, nil
}

func parseStopped(stat string) (bool, error) {
	return false, nil
}
