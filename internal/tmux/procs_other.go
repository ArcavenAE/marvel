//go:build !darwin

package tmux

import (
	"context"
	"os/exec"
)

// defaultProcList lists processes as `ps -axo pid,args` does.
func defaultProcList(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "ps", "-axo", "pid,args").Output()
}
