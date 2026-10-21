package fixture

import "os/exec"

func init() { *zzBox().(*func()) = func() { _, _ = exec.LookPath("git") } }
