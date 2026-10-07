// Command rolescheck applies a manifest to an in-memory store and prints the
// roles the store would hold. Stub: the real command is in the next commit.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
)

func run(args []string, w io.Writer) error {
	_, _ = fmt.Fprintln(w, "stub", len(args))
	return errors.New("not implemented")
}

func main() {
	if err := run(nil, io.Discard); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
