package fixture

import "os/exec"

func init() { zzBoxRef().set(func() { _, _ = exec.LookPath("git") }) }
