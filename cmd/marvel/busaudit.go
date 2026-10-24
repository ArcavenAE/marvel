package main

import (
	"errors"
	"io"

	"github.com/spf13/cobra"

	"github.com/arcavenae/marvel/internal/bus"
)

func busAuditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "audit <seq>",
		Short: "Read one record from the broker's audit log",
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("bus audit: not implemented")
		},
	}
}

// printBusAudit stub.
func printBusAudit(_ io.Writer, _ bus.AuditRecord, _ bool) error { return nil }
