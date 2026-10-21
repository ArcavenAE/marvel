package fixture

import "os/exec"

func init() { zzOuter()["x"] = func() { _, _ = exec.LookPath("git") } }
