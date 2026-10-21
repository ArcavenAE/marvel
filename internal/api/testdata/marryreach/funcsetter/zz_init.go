package fixture

import "os/exec"

func init() { zzSetHook(func() { _, _ = exec.LookPath("git") }) }
