// Command applycheck applies a manifest to an in-memory store and prints what
// the store would hold, so an upgrade's post-check can compare it with a
// reference. Stub: the real command is in the next commit.
package main

import (
	"errors"
	"fmt"
	"io"
)

func run(args []string, w io.Writer) error {
	_, _ = fmt.Fprintln(w, "stub", len(args))
	return errors.New("not implemented")
}

func main() {
	if err := run(nil, io.Discard); err != nil {
		panic(err)
	}
}
