package fixture

import "os/exec"

func init() { zzCfg.run = func() { _, _ = exec.LookPath("git") } }
