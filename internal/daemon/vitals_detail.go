package daemon

import (
	"fmt"
	"io/fs"
	"os"

	"github.com/arcavenae/marvel/internal/hostvitals"
	"github.com/arcavenae/marvel/internal/procargs"
)

// detailReaders reads the arguments and working directory of a process for the
// unattributed orphan report.
type detailReaders struct {
	args  func(pid int) ([]string, error)
	cwd   func(pid int) (string, error)
	owner func(pid int) (int, error)
	self  int
}

// hostDetailReaders reads from the kernel as the daemon's own user.
func hostDetailReaders() detailReaders {
	return detailReaders{args: procargs.Read, cwd: procargs.Cwd, owner: procargs.OwnerUID, self: os.Getuid()}
}

// read reads the arguments and directory of a process. The errors a host
// returns for a process of another user are not recognisable as such: macOS
// gives the sysctl errno EINVAL and lsof exits 1. So when either read fails, the
// process's uid is compared with the daemon's, and for another user's process the
// errors are wrapped as fs.ErrPermission, which the report prints as
// "unreadable".
func (r detailReaders) read(pid int) hostvitals.ProcDetail {
	var d hostvitals.ProcDetail
	d.Args, d.ArgsErr = r.args(pid)
	d.Cwd, d.CwdErr = r.cwd(pid)
	if d.ArgsErr == nil && d.CwdErr == nil {
		return d
	}
	if uid, err := r.owner(pid); err == nil && uid != r.self {
		if d.ArgsErr != nil {
			d.ArgsErr = fmt.Errorf("%w: process of another user: %v", fs.ErrPermission, d.ArgsErr)
		}
		if d.CwdErr != nil {
			d.CwdErr = fmt.Errorf("%w: process of another user: %v", fs.ErrPermission, d.CwdErr)
		}
	}
	return d
}
