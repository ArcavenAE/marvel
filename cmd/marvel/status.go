package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/suspend"
	"github.com/spf13/cobra"
)

// statusNow is the clock status measures elapsed time to; a test replaces it.
var statusNow = time.Now

// statusCmd is marvel status: whether the cluster is running or suspended
// (docs/design/fleet-suspend-resume.md section 4). The daemon answers from its
// live record without its controller lock or tmux, so it answers while the
// fleet is frozen; with the daemon down, the mirror beside the socket answers.
func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Say whether the cluster is running or suspended",
		Long: `Say whether the cluster is running or suspended.

The daemon answers from its suspend record without taking its controller lock
or calling tmux, so status answers while the fleet is frozen. If the daemon
does not answer, status reads the mirror the daemon keeps beside its socket,
and says so. A TCP or mrvl:// address has no mirror on this host.

A suspended cluster lists the manual kill -CONT recipe: for the operator to
run by hand only if marvel cannot.

Exit codes: 0 running, 3 suspended, partway through a suspend or resume, or
unknown (the stored record could not be read; the mirror is left in place),
1 error.`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			addr, _, err := resolveDaemon()
			if err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				return &exitError{Code: 1, Msg: err.Error()}
			}
			resp, sendErr := send(daemonrpc.Request{Method: "suspend.status"})
			code, err := reportStatus(cmd.OutOrStdout(), addr, resp, sendErr, statusNow())
			if err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
			}
			if code != 0 {
				return &exitError{Code: code, Msg: "cluster not running"}
			}
			return nil
		},
	}
}

// reportStatus writes the status for addr from the daemon's answer, or from
// the mirror when the daemon did not answer, and returns the exit code.
func reportStatus(w io.Writer, addr string, resp *daemonrpc.Response, sendErr error, now time.Time) (int, error) {
	if sendErr != nil {
		mirror := suspend.MirrorPath(addr)
		if mirror == "" {
			return 1, fmt.Errorf("daemon at %s is not answering (%v), and a TCP address has no suspend mirror on this host", addr, sendErr)
		}
		rec, found, err := suspend.ReadMirror(mirror)
		if err != nil {
			return 1, fmt.Errorf("daemon at %s is not answering (%v), and its mirror cannot be read: %w", addr, sendErr, err)
		}
		if !found {
			return 1, fmt.Errorf("daemon at %s is not answering (%v); no suspend mirror beside its socket, so it recorded no suspend", addr, sendErr)
		}
		st := suspend.StatusOf(&rec)
		if _, err := fmt.Fprint(w, suspend.Render(st, now, true)); err != nil {
			return 1, fmt.Errorf("write status: %w", err)
		}
		return st.ExitCode(), nil
	}
	if resp.Error != "" {
		if strings.HasPrefix(resp.Error, "unknown method") {
			return 1, fmt.Errorf("the daemon at %s predates marvel status; it cannot report a suspend", addr)
		}
		return 1, fmt.Errorf("%s", resp.Error)
	}
	var st suspend.Status
	if err := json.Unmarshal(resp.Result, &st); err != nil {
		return 1, fmt.Errorf("decode suspend status: %w", err)
	}
	st.Unsettled = drawn(st.Unsettled)
	if _, err := fmt.Fprint(w, suspend.Render(st, now, false)); err != nil {
		return 1, fmt.Errorf("write status: %w", err)
	}
	return st.ExitCode(), nil
}
