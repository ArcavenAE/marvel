//go:build darwin

package suspend

// sstop is p_stat for a stopped process (SSTOP in xnu's sys/proc.h, where
// SIDL is 1, SRUN 2, SSLEEP 3, SSTOP 4 and SZOMB 5). It is what ps prints as T.
const sstop = 4

// readStopped reads p_stat through kern.proc.pid. No ps, so it works where a
// setuid ps cannot run.
func readStopped(pid int) (bool, error) {
	return false, nil
}
