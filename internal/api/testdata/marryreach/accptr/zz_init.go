package fixture

import "os/exec"

func init() { *zzHookPtr() = func() { _, _ = exec.LookPath("git") } }
