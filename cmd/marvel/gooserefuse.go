package main

import (
	"fmt"
	"regexp"

	"github.com/spf13/cobra"
)

// exitGooseRefused is the exit code of a refused goose launch. It is
// marvel-reserved: goose exits 1 for every failure and never returns 78, so the
// reap can tell a refusal from a goose failure (goose-adapter-v1.md G8).
const exitGooseRefused = 78

// gooseReasonShape bounds the reason the stub prints to the slug form the
// adapter's refusal reasons take, so the stub writes nothing else to a pane.
var gooseReasonShape = regexp.MustCompile(`^[a-z][a-z-]{0,63}$`)

// newGooseRefuseCmd is the refusal stub the goose adapter launches in place of
// goose when a posture rule refuses the launch. It never runs goose: it writes
// the reason to stderr and exits 78. A Prepare error is not an option, because
// the manager falls back to the raw command on one, which would start goose at
// whatever GOOSE_MODE the host has (goose-adapter-v1.md section 4).
func newGooseRefuseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "goose-refuse <reason>",
		Short:         "Refuse a goose launch: print the reason and exit 78 (internal)",
		Hidden:        true,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			reason := args[0]
			if !gooseReasonShape.MatchString(reason) {
				reason = "unreadable-reason"
			}
			// A pane that cannot take the line still gets the exit code.
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "marvel: goose launch refused: %s\n", reason)
			return &exitError{Code: exitGooseRefused, Msg: "goose launch refused: " + reason}
		},
	}
	return cmd
}
