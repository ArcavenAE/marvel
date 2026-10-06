package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/arcavenae/marvel/internal/daemon"
)

// daemonDescription is what `marvel describe daemon` prints. Address and
// rung are the client's own facts: which socket it dialed and which step of
// the resolution order chose it. The embedded status is the daemon's own
// record from one daemon.status call, the same call the get sessions header
// reads. Diagnostic only: nothing gates on any of it.
type daemonDescription struct {
	Address string `json:"address"`
	Rung    string `json:"rung"`
	daemon.DaemonStatus
}

// describeDaemon prints what `marvel describe daemon` shows: the address
// this client resolved, the rung that chose it, and the daemon's own
// status record from one daemon.status call.
func describeDaemon(w io.Writer) error {
	addr, _, rung, err := resolveDaemonRung()
	if err != nil {
		return err
	}
	resp, err := send(daemon.Request{Method: "daemon.status"})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	d := daemonDescription{Address: addr, Rung: string(rung)}
	if err := json.Unmarshal(resp.Result, &d.DaemonStatus); err != nil {
		return fmt.Errorf("decode daemon status: %w", err)
	}
	out, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(out))
	return err
}

// describeArgs is the argument rule for describe: every resource takes a
// name except daemon, which describes the one daemon answering and takes none.
func describeArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 && args[0] == "daemon" {
		if len(args) != 1 {
			return fmt.Errorf("describe daemon takes no name, received %d argument(s)", len(args)-1)
		}
		return nil
	}
	return cobra.ExactArgs(2)(cmd, args)
}
