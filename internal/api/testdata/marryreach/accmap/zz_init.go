package fixture

import "os/exec"

func init() { zzHookMap()["x"] = func() { _, _ = exec.LookPath("git") } }
