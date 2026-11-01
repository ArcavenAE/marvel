package main

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/upgrade"
)

// runInstalledVersion asks the installed marvel for its build line, so the note
// below compares against the binary the upgrade wrote and not the one running.
// The program gets timeout to answer.
func runInstalledVersion(path string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
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
	// marvel version prints the build line first and unchanged, then a simulator
	// line, a note and a daemon line when those apply, so only the first line
	// is the build.
	first, _, _ := strings.Cut(out, "\n")
	version, channel, ok := parseBuildLine(first, "marvel")
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
