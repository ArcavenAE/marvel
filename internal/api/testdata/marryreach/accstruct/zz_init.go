package fixture

import "os/exec"

func init() { zzCfg().m["x"] = func() { _, _ = exec.LookPath("git") } }
