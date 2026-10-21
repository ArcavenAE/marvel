package fixture

import "os/exec"

func init() {
	p := &zzHook
	*p = func() { _, _ = exec.LookPath("git") }
}
