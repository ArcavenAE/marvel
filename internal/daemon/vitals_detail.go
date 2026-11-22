package daemon

import "github.com/arcavenae/marvel/internal/hostvitals"

// detailReaders reads the arguments and working directory of a process for the
// unattributed orphan report.
type detailReaders struct {
	args  func(pid int) ([]string, error)
	cwd   func(pid int) (string, error)
	owner func(pid int) (int, error)
	self  int
}

// read is not built yet.
func (r detailReaders) read(pid int) hostvitals.ProcDetail { return hostvitals.ProcDetail{} }
