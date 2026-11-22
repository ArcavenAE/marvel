package daemon

import (
	"errors"
	"io/fs"
	"os/exec"
	"syscall"
	"testing"
)

// The errors a host really returns for a process of another user: macOS gives
// the sysctl errno EINVAL for kern.procargs2 and lsof exits 1; Linux gives
// EACCES. None of them is fs.ErrPermission on its own, so the reader compares
// the target's uid with its own and says so.
func realErrors() (argsErr, cwdErr error) {
	return syscall.EINVAL, &exec.ExitError{}
}

func TestAProcessOfAnotherUserReadsAsUnreadable(t *testing.T) {
	argsErr, cwdErr := realErrors()
	r := detailReaders{
		args:  func(int) ([]string, error) { return nil, argsErr },
		cwd:   func(int) (string, error) { return "", cwdErr },
		owner: func(int) (int, error) { return 0, nil },
		self:  503,
	}
	d := r.read(1)
	if !errors.Is(d.ArgsErr, fs.ErrPermission) || !errors.Is(d.CwdErr, fs.ErrPermission) {
		t.Errorf("errors = %v, %v, want both to be fs.ErrPermission for a process of uid 0 read as uid 503", d.ArgsErr, d.CwdErr)
	}
}

func TestAFailureOnAProcessOfTheSameUserIsNotAPermissionError(t *testing.T) {
	argsErr, cwdErr := realErrors()
	r := detailReaders{
		args:  func(int) ([]string, error) { return nil, argsErr },
		cwd:   func(int) (string, error) { return "", cwdErr },
		owner: func(int) (int, error) { return 503, nil },
		self:  503,
	}
	d := r.read(4242)
	if d.ArgsErr == nil || d.CwdErr == nil {
		t.Fatalf("errors = %v, %v, want both kept", d.ArgsErr, d.CwdErr)
	}
	if errors.Is(d.ArgsErr, fs.ErrPermission) || errors.Is(d.CwdErr, fs.ErrPermission) {
		t.Errorf("errors = %v, %v, want no permission error for a process of the same user", d.ArgsErr, d.CwdErr)
	}
}

func TestAReadableProcessOfAnotherUserKeepsItsDetail(t *testing.T) {
	r := detailReaders{
		args:  func(int) ([]string, error) { return []string{"sleep", "9"}, nil },
		cwd:   func(int) (string, error) { return "/srv/job", nil },
		owner: func(int) (int, error) { return 0, nil },
		self:  0,
	}
	d := r.read(7)
	if d.ArgsErr != nil || d.CwdErr != nil || len(d.Args) != 2 || d.Cwd != "/srv/job" {
		t.Errorf("detail = %+v, want the args and cwd as read", d)
	}
}
