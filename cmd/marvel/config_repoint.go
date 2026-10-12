package main

import (
	"errors"
	"io"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

// repointCmd is `marvel config repoint`.
func repointCmd() *cobra.Command {
	return &cobra.Command{
		Use:  "repoint <cluster> <address>",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("not implemented")
		},
	}
}

// repointProbe reads the host key an address presents; a test replaces it.
var repointProbe = func(string) (ssh.PublicKey, error) { return nil, errors.New("not implemented") }

// runRepoint moves cluster name to newAddr when the key newAddr presents is
// one known_hosts already holds for the cluster's current address.
func runRepoint(out io.Writer, in io.Reader, interactive bool, name, newAddr string) error {
	return errors.New("not implemented")
}
