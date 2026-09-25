// Package workload spawns the managed children the daemon supervises: a
// process that is not a Role, has no pane, and must not inherit the
// daemon's environment (docs/design/services-list.md section 3,
// aae-orc-oo62t).
//
// The daemon is started from an operator shell, and that shell carries
// whatever the operator exported for other work: the bd client password,
// a heartbeat token, cloud and vendor keys. A managed child needs none of
// them. Start is the one way a managed child spawns, and its environment
// is built in one function from two inputs only: keys the class contract
// allows by name, and secrets the daemon minted for this child. There is
// no config field that widens it.
package workload

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

// ProcessSpec describes one managed child.
type ProcessSpec struct {
	// Name labels the child in errors and the daemon log ("bus").
	Name string
	// Binary is the resolved executable; the driver resolves it.
	Binary string
	// Args follow the binary ("-c", confPath).
	Args []string
	// EnvAllow names the daemon environment keys the child may inherit.
	// It comes from the class contract (service.ClassContract.ChildEnvAllow)
	// plus any constant a driver adds in reviewed code, never from config.
	EnvAllow []string
	// MintedEnv yields KEY=VALUE secrets the daemon minted for this child,
	// read fresh at every spawn. Nil means none.
	MintedEnv func() []string
	// Check sees the child environment before the child starts and may
	// refuse it; a driver's conf guard lives here. Nil accepts.
	Check func(env []string) error
	// LogPath receives the child's stdout and stderr, appended, mode 0600.
	LogPath string
	// PidFile, when set, records the child pid for adoption by a
	// successor daemon. Written 0644 on a best-effort basis.
	PidFile string
}

// Child is a started managed child.
type Child struct {
	Cmd *exec.Cmd
	Pid int
	// Env is the environment the child was started with. Drivers read it
	// to record what the running process can resolve (the leaf seed).
	Env []string
}

// Start builds the child environment, runs Check, and starts the child in
// its own process group with its output in LogPath.
func Start(spec ProcessSpec) (*Child, error) {
	var minted []string
	if spec.MintedEnv != nil {
		minted = spec.MintedEnv()
	}
	env := childEnv(spec.EnvAllow, minted)
	if spec.Check != nil {
		if err := spec.Check(env); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(spec.LogPath), 0o700); err != nil {
		return nil, fmt.Errorf("%s: create log dir: %w", spec.Name, err)
	}
	logf, err := os.OpenFile(spec.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("%s: open %s: %w", spec.Name, spec.LogPath, err)
	}
	cmd := exec.Command(spec.Binary, spec.Args...)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = logf.Close()
		return nil, fmt.Errorf("%s: start %s: %w", spec.Name, filepath.Base(spec.Binary), err)
	}
	_ = logf.Close() // the child holds its own descriptor
	pid := cmd.Process.Pid
	if spec.PidFile != "" {
		if err := os.MkdirAll(filepath.Dir(spec.PidFile), 0o700); err == nil {
			if werr := os.WriteFile(spec.PidFile, []byte(strconv.Itoa(pid)+"\n"), 0o644); werr != nil {
				log.Printf("%s: write pidfile %s: %v", spec.Name, spec.PidFile, werr)
			}
		}
	}
	return &Child{Cmd: cmd, Pid: pid, Env: env}, nil
}

// childEnv is the only constructor of a managed child's environment: the
// allowed keys the daemon actually has, then the minted secrets. A minted
// key that repeats an allowed one comes last and wins, as exec resolves it.
func childEnv(allow, minted []string) []string {
	env := make([]string, 0, len(allow)+len(minted))
	for _, k := range allow {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return append(env, minted...)
}
