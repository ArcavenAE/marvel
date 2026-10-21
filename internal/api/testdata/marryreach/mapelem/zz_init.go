package fixture

import "os/exec"

func init() { zzHooks["x"] = func() { _, _ = exec.LookPath("git") } }
