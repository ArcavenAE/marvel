package main

import (
	"io"

	"github.com/arcavenae/marvel/internal/upgrade"
)

// afterUpgrade decides what follows an upgrade that returned no error:
// whether to re-exec the running daemon. Scaffold.
func afterUpgrade(res upgrade.Result, reexecDaemon bool, reexec func() error, w io.Writer) error {
	_ = res
	_ = w
	if !reexecDaemon {
		return nil
	}
	return reexec()
}
