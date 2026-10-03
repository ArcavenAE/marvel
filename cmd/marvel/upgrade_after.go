package main

import (
	"fmt"
	"io"

	"github.com/arcavenae/marvel/internal/upgrade"
)

// afterUpgrade decides what follows an upgrade that returned no error. With
// --daemon it re-executes the running daemon, but only into a binary that
// changed on disk: an upgrade that installed nothing must not claim the daemon
// adopted a new build.
func afterUpgrade(res upgrade.Result, reexecDaemon bool, reexec func() error, w io.Writer) error {
	if !reexecDaemon {
		return nil
	}
	if !res.Changed {
		_, _ = fmt.Fprintln(w, "the installed binary did not change, so the daemon was not re-executed; "+
			"to adopt the installed binary run 'marvel daemon reexec'")
		return nil
	}
	if err := reexec(); err != nil {
		return fmt.Errorf("binary upgraded, but daemon re-exec failed: %w", err)
	}
	_, _ = fmt.Fprintln(w, "running daemon re-executing in place; agents keep running")
	return nil
}
