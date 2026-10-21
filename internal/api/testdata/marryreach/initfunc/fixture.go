// Package fixture: Marry calls a function value that an init assigns.
package fixture

import "os/exec"

var zzHook = func() {}

func init() { zzHook = func() { _, _ = exec.LookPath("git") } }

func Marry() { zzHook() }
