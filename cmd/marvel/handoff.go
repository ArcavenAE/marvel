package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/spf13/cobra"
)

func handoffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "handoff",
		Short: "Hand a seat's work to its successor through the daemon",
	}
	cmd.AddCommand(handoffPutCmd())
	return cmd
}

func handoffPutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "put",
		Short: "Write this seat's handoff through the daemon (text on stdin)",
		Long: `Send the handoff text on stdin to the marvel daemon, which writes the file the
role declares for it, ending with the role's marker.

For a seat that cannot write a file on the daemon host (a read-only sandbox, or
a deny-Write permission) but can run this one command. The seat's own heartbeat
token, from the environment marvel started it with, says whose handoff it is,
so the command takes no session name and cannot write for another seat. The
daemon accepts it only while the seat holds a pending max-age handoff request,
and only on its own local socket.

The text is capped at 64 KiB. Run it when the seat is asked for a handoff:

  marvel handoff put <<'EOF'
  ...what was done, what is left, what the successor needs...
  EOF`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return handoffPut(cmd.InOrStdin(), cmd.OutOrStdout(), api.HeartbeatTokenFromEnv(), send)
		},
	}
}

// handoffPut sends the handoff text on in to the daemon for the session that
// owns token and prints the file the daemon wrote. Input that is empty or over
// the cap is refused before anything is sent.
func handoffPut(in io.Reader, out io.Writer, token string, send func(daemonrpc.Request) (*daemonrpc.Response, error)) error {
	if token == "" {
		return fmt.Errorf("no heartbeat token: %s (or %s) is not set; run this from the seat marvel started",
			api.HeartbeatTokenEnv, api.HeartbeatTokenFileEnv)
	}
	b, err := io.ReadAll(io.LimitReader(in, api.MaxHandoffPutBytes+1))
	if err != nil {
		return fmt.Errorf("read handoff text: %w", err)
	}
	if len(b) > api.MaxHandoffPutBytes {
		return fmt.Errorf("handoff text is too large: the limit is %d bytes", api.MaxHandoffPutBytes)
	}
	if strings.TrimSpace(string(b)) == "" {
		return fmt.Errorf("handoff text is empty: put the handoff on stdin")
	}
	params, err := json.Marshal(api.HandoffPutRequest{Token: token, Text: string(b)})
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	resp, err := send(daemonrpc.Request{Method: "handoff.put", Params: params})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	var got api.HandoffPutResult
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		return fmt.Errorf("decode handoff.put result: %w", err)
	}
	_, _ = fmt.Fprintf(out, "handoff written: %s (%d bytes)\n", got.Path, got.Bytes)
	return nil
}
