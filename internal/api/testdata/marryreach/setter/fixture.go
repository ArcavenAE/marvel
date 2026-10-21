// Package fixture: the implementation reaches the interface only through a
// setter that another package calls, so only the interface's implementations
// lead to os/exec.
package fixture

import "os/exec"

type zzRunner interface{ zzRun() }

type zzReal struct{}

func (zzReal) zzRun() { _, _ = exec.LookPath("git") }

var zzImpl zzRunner

// SetRunner is called from outside the package.
func SetRunner(r zzRunner) { zzImpl = r }

// Default is what that caller passes.
func Default() zzRunner { return zzReal{} }

func Marry() {
	if zzImpl != nil {
		zzImpl.zzRun()
	}
}
