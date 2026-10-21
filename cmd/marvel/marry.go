package main

import (
	"github.com/spf13/cobra"
)

// marryCmd is the read-only CLI for api.Marry.
func marryCmd() *cobra.Command {
	var input string
	cmd := &cobra.Command{
		Use:          "marry --input <file|->",
		Short:        "Pair ready work items with free seats, read-only",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return nil
		},
	}
	cmd.Flags().StringVar(&input, "input", "", `the MarryInput JSON file, or "-" for stdin`)
	_ = cmd.MarkFlagRequired("input")
	return cmd
}
