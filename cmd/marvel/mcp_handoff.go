package main

import (
	"errors"
	"io"
	"time"

	"github.com/spf13/cobra"
)

// mcpHandoffOptions carries what the server reads from outside: the
// environment (its own, never the root flags), the exchange timeout and the
// build version reported in serverInfo.
type mcpHandoffOptions struct {
	Env     func(string) string
	Timeout time.Duration
	Version string
}

func mcpCmd() *cobra.Command {
	return &cobra.Command{Use: "mcp", Short: "MCP servers marvel offers to its seats"}
}

// serveMCPHandoff serves the submit_handoff tool on in and out.
func serveMCPHandoff(in io.Reader, out, errw io.Writer, opt mcpHandoffOptions) error {
	return errors.New("not implemented")
}
