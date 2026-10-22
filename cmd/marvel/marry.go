package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/spf13/cobra"
)

// marryCmd is the read-only CLI for api.Marry, so a caller outside this
// module (director's idle sweep, M2) runs marvel's marrying step instead of
// keeping a copy. It reads no daemon state and opens no socket: it decodes a
// MarryInput, calls api.Marry and prints the MarryOutput.
func marryCmd() *cobra.Command {
	var input string
	cmd := &cobra.Command{
		Use:   "marry --input <file|->",
		Short: "Pair ready work items with free seats, read-only",
		Long: `Pair ready work items with free seats, read-only.

Reads a MarryInput as JSON from --input (a file, or "-" for stdin), runs
marvel's marrying step on it, and prints the MarryOutput as JSON on stdout.
The shapes are the contract in contracts/readiness; a field the contract does
not have is refused.

It does not contact the daemon and sends nothing: no inject, no message, no
bd or gh call. An input error (a repeated item or seat, malformed JSON) goes
to stderr with a nonzero exit and nothing on stdout.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMarry(input, cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&input, "input", "", `the MarryInput JSON file, or "-" for stdin`)
	_ = cmd.MarkFlagRequired("input")
	return cmd
}

// runMarry reads one MarryInput document from input, or from stdin when input
// is "-", and writes the result to stdout only when the step succeeds.
func runMarry(input string, stdin io.Reader, stdout io.Writer) error {
	r := stdin
	if input != "-" {
		f, err := os.Open(input)
		if err != nil {
			return fmt.Errorf("marry input: %w", err)
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var in api.MarryInput
	if err := dec.Decode(&in); err != nil {
		return fmt.Errorf("marry input: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("marry input: more than one JSON document")
	}
	out, err := api.Marry(in)
	if err != nil {
		return err
	}
	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s\n", b)
	return err
}
