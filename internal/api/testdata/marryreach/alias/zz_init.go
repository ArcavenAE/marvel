package fixture

import "os/exec"

func init() {
	m := zzHooks
	m["x"] = func() { _, _ = exec.LookPath("git") }
}
