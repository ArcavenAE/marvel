package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/spf13/cobra"
)

func runSandboxedCmd() *cobra.Command {
	var profile, dir string
	var timeout int
	cmd := &cobra.Command{
		Use:   "run-sandboxed [flags] -- <program> [args...]",
		Short: "Run a command under a sandbox profile the daemon composes, outside this seat's own profile",
		Long: `Ask the marvel daemon to run a command for this seat. A seat under marvel's
default privacy deny cannot start its own sandbox-exec (a nested profile that
differs from the outer one is refused, exit 71); the daemon is not under the
seat's profile, so it runs the command instead and returns the output and
exit status.

The daemon never applies the profile on its own. It composes the role's own
profile, then the requested profile, then the privacy deny block, and refuses a
request outside a closed grammar: deny forms, plus the three localhost network
allows the run conditions use, and no (version), (allow default), (define),
(import), (param) or comment tricks. The seat is identified by its heartbeat
token, from the environment marvel started it with.

The command runs with this process's environment (minus the heartbeat token)
and working directory, whole output at exit, no stdin and no tty, capped at 1 MiB
per stream. The daemon ends the run when this connection reaches end of file, so
the client must keep its side of the socket open until the answer comes (this
command does). This command exits with the command's own status; a daemon refusal
exits 1 with the reason on stderr.

  marvel run-sandboxed --profile run.sb -- /usr/bin/env go test ./...`,
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var text string
			if profile != "" {
				b, err := os.ReadFile(profile)
				if err != nil {
					return fmt.Errorf("read profile: %w", err)
				}
				text = string(b)
			}
			if dir == "" {
				wd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("working directory: %w", err)
				}
				dir = wd
			}
			rc, err := runSandboxed(cmd.OutOrStdout(), cmd.ErrOrStderr(), api.HeartbeatTokenFromEnv(), args, dir, os.Environ(), text, timeout, send)
			if err != nil {
				return err
			}
			if rc != 0 {
				cmd.SilenceErrors = true
				return &exitError{Code: rc}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&profile, "profile", "", "file holding the run profile (deny forms and the localhost network allows)")
	cmd.Flags().StringVar(&dir, "dir", "", "working directory for the command (default: this one)")
	cmd.Flags().IntVar(&timeout, "timeout", 0, "seconds before the daemon kills the command (default 600, at most 3600)")
	return cmd
}

// runSandboxed sends one run.sandboxed request and writes the command's output
// to out and errw. It returns the command's exit status. The heartbeat token
// never goes into the command's environment.
func runSandboxed(out, errw io.Writer, token string, argv []string, dir string, environ []string, profile string, timeout int,
	send func(daemonrpc.Request) (*daemonrpc.Response, error),
) (int, error) {
	if token == "" {
		return 0, fmt.Errorf("no heartbeat token: %s (or %s) is not set; run this from the seat marvel started",
			api.HeartbeatTokenEnv, api.HeartbeatTokenFileEnv)
	}
	var env []string
	for _, kv := range environ {
		if strings.HasPrefix(kv, api.HeartbeatTokenEnv+"=") || strings.HasPrefix(kv, api.HeartbeatTokenFileEnv+"=") {
			continue
		}
		env = append(env, kv)
	}
	params, err := json.Marshal(api.RunSandboxedRequest{
		Token: token, Argv: argv, Dir: dir, Env: env, Profile: profile, TimeoutSeconds: timeout,
	})
	if err != nil {
		return 0, fmt.Errorf("encode request: %w", err)
	}
	resp, err := send(daemonrpc.Request{Method: daemonrpc.MethodRunSandboxed, Params: params})
	if err != nil {
		return 0, err
	}
	if resp.Error != "" {
		return 0, fmt.Errorf("%s", resp.Error)
	}
	var res api.RunSandboxedResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		return 0, fmt.Errorf("decode run.sandboxed result: %w", err)
	}
	_, _ = io.WriteString(out, res.Stdout)
	_, _ = io.WriteString(errw, res.Stderr)
	if res.Truncated {
		_, _ = fmt.Fprintf(errw, "marvel: output was cut at %d bytes per stream\n", api.MaxRunOutputBytes)
	}
	if res.TimedOut {
		_, _ = fmt.Fprintln(errw, "marvel: the daemon killed the command at its time limit")
	}
	return res.ExitCode, nil
}
