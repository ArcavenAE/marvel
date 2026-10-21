package fixture

import "os/exec"

var zzKey = func() string { p, _ := exec.LookPath("git"); return p }()
