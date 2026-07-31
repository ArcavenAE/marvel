package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
)

// runTee dumps the stream socket verbatim, for checks that need the raw child
// output rather than sink's spew accounting.
func runTee(args []string) error {
	fs := flag.NewFlagSet("tee", flag.ExitOnError)
	sock := fs.String("s", "", "stream socket path")
	out := fs.String("o", "", "write to this file instead of stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sock == "" {
		return fmt.Errorf("-s is required")
	}
	conn, err := net.Dial("unix", *sock)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	w := io.Writer(os.Stdout)
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		w = f
	}
	_, err = io.Copy(w, conn)
	return err
}
