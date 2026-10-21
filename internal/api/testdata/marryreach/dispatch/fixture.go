// Package fixture: Marry reaches os/exec only through an interface method
// whose implementation is installed in an init.
package fixture

import "os/exec"

type zzRunner interface{ zzRun() }

type zzReal struct{}

func (zzReal) zzRun() { _, _ = exec.LookPath("git") }

var zzImpl zzRunner

func init() { zzImpl = zzReal{} }

func zzPick() zzRunner { return zzImpl }

func Marry() { zzPick().zzRun() }
