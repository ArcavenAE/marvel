package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// capWriter keeps the first max bytes and counts the rest as dropped, so a
// chatty child never blocks on a full pipe.
type capWriter struct {
	buf     bytes.Buffer
	max     int
	dropped bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	room := w.max - w.buf.Len()
	if room > len(p) {
		room = len(p)
	}
	if room > 0 {
		w.buf.Write(p[:room])
	}
	if room < len(p) {
		w.dropped = true
	}
	return len(p), nil
}

// RunChild runs argv in its own process group and returns when it exits. The
// group is killed at the timeout and when ctx ends, and each stream is capped
// at maxOut bytes. A command that cannot be started is an error, never a
// result with an exit status.
func RunChild(ctx context.Context, argv []string, dir string, env []string, timeout time.Duration, maxOut int) (api.RunSandboxedResult, error) {
	if len(argv) == 0 {
		return api.RunSandboxedResult{}, errors.New("no command")
	}
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // the program is the seat's request, run under a composed profile
	cmd.Dir = dir
	cmd.Env = append([]string{}, env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	out, errw := &capWriter{max: maxOut}, &capWriter{max: maxOut}
	cmd.Stdout, cmd.Stderr = out, errw
	if err := cmd.Start(); err != nil {
		return api.RunSandboxedResult{}, fmt.Errorf("start %s: %w", argv[0], err)
	}
	pgid := cmd.Process.Pid
	var limit <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		limit = t.C
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timedOut := false
	var werr error
	select {
	case werr = <-done:
	case <-limit:
		timedOut = true
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		werr = <-done
	case <-ctx.Done():
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		werr = <-done
	}
	// A group member that outlived the leader dies with the group.
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	res := api.RunSandboxedResult{
		Stdout: out.buf.String(), Stderr: errw.buf.String(),
		Truncated: out.dropped || errw.dropped, TimedOut: timedOut,
	}
	var ee *exec.ExitError
	switch {
	case werr == nil:
	case errors.As(werr, &ee):
		res.ExitCode = ee.ExitCode()
	case errors.Is(werr, exec.ErrWaitDelay):
	default:
		return res, fmt.Errorf("wait %s: %w", argv[0], werr)
	}
	return res, nil
}
