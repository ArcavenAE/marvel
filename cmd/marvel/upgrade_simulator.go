package main

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	"github.com/arcavenae/marvel/internal/upgrade"
)

// runInstalledVersion asks the installed marvel for its build line, so the note
// below compares against the binary the upgrade wrote and not the one running.
func runInstalledVersion(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), simulatorVersionTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").Output()
	return string(out), err
}

// noteStaleSimulator prints one line after a direct-binary upgrade when the
// marvel-simulator on PATH is a different build from the marvel just installed.
// A release download carries marvel alone, so the simulator stays as it was; a
// Homebrew install replaces both and prints nothing here. Nothing is
// downloaded, and nothing is printed when no simulator is on PATH or either
// program does not report its build. look, runSim and runMarvel are injected so
// a test needs no program on PATH.
func noteStaleSimulator(w io.Writer, res upgrade.Result, look func(string) (string, error), runSim, runMarvel func(string) (string, error)) {
	if !res.Direct || !res.Changed {
		return
	}
	simPath, err := look("marvel-simulator")
	if err != nil {
		return
	}
	out, err := runMarvel(res.Binary)
	if err != nil {
		return
	}
	version, channel, ok := parseBuildLine(out, "marvel")
	if !ok {
		return
	}
	out, err = runSim(simPath)
	if err != nil {
		return
	}
	simVersion, simChannel, ok := parseBuildLine(out, "marvel-simulator")
	if !ok || (simVersion == version && simChannel == channel) {
		return
	}
	_, _ = fmt.Fprintf(w, "note: the marvel-simulator on PATH is %s (%s) and marvel is now %s (%s); "+
		"update it with 'brew upgrade arcavenae/tap/marvel' or from the release archive\n",
		simVersion, simChannel, version, channel)
}
