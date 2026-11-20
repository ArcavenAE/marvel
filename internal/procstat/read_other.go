//go:build !darwin && !linux

package procstat

// readProcesses has no implementation outside darwin and linux. marvel
// builds for those two; anywhere else the sampler stays quiet instead of
// reporting invented numbers.
func readProcesses() ([]entry, error) {
	return nil, ErrUnsupported
}

// readProcessesWide has no implementation here either.
func readProcessesWide() ([]entry, []Proc, error) {
	return nil, nil, ErrUnsupported
}
