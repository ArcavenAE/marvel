package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/arcavenae/marvel/internal/goosewatch"
)

// newGooseWatchCmd is the watchdog wrapper a headless goose seat launches goose
// under (goose-adapter-v1.1.md rule C3). It runs the command after `--` as a
// child in its own process group, copies the child's stdout to its own, and
// ends the run on a wall-clock or idle limit. The logic is internal/goosewatch;
// this is the flag surface. Like goose-refuse it is hidden: only the goose
// adapter's launch line names it.
func newGooseWatchCmd() *cobra.Command {
	var (
		wall, idle, grace time.Duration
		stderrFile        string
		stderrCap         int
	)
	cmd := &cobra.Command{
		Use:           "goose-watch --wall D --idle D --grace D [--stderr-file F] [--stderr-cap N] -- <command> [args...]",
		Short:         "Run a goose command under the watchdog (internal)",
		Hidden:        true,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.ArgsLenAtDash() != 0 || len(args) == 0 {
				return &exitError{Code: goosewatch.ExitCannotRun, Msg: "goose-watch: the command to run goes after --"}
			}
			code := goosewatch.Run(goosewatch.Config{
				Argv:       args,
				Wall:       wall,
				Idle:       idle,
				Grace:      grace,
				StderrFile: stderrFile,
				StderrCap:  stderrCap,
				Stdin:      cmd.InOrStdin(),
				Stdout:     cmd.OutOrStdout(),
				Stderr:     cmd.ErrOrStderr(),
			})
			if code == 0 {
				return nil
			}
			return &exitError{Code: code, Msg: fmt.Sprintf("goose-watch: exit %d", code)}
		},
	}
	f := cmd.Flags()
	f.DurationVar(&wall, "wall", 0, "wall-clock limit for the run")
	f.DurationVar(&idle, "idle", 0, "idle limit: no stream line for this long, paused while a tool call is open")
	f.DurationVar(&grace, "grace", 0, "how long the command has after TERM before KILL")
	f.StringVar(&stderrFile, "stderr-file", "", "file that receives the command's stderr, held to --stderr-cap")
	f.IntVar(&stderrCap, "stderr-cap", 0, "size limit of the stderr file in bytes (default 65536)")
	return cmd
}
