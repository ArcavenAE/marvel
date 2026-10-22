package fixture

import "os/exec"

func init() { zzSetter(func() { _, _ = exec.LookPath("git") }) }
