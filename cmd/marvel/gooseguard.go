package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/arcavenae/marvel/internal/goosewatch"
)

// guardPipeFD is the descriptor the wrapper hands the guard: the read end of the
// pipe only the wrapper writes (exec.Cmd.ExtraFiles[0] becomes fd 3).
const guardPipeFD = 3

// newGooseGuardCmd is the guard process `marvel goose-watch` starts in its own
// session. It outlives a SIGKILL of the wrapper and ends goose's process group
// when the pipe closes without a "done" line. The logic is internal/goosewatch;
// this is the flag surface. Hidden like goose-watch: only the wrapper starts it.
func newGooseGuardCmd() *cobra.Command {
	var (
		grace      time.Duration
		stderrFile string
		arg        string
	)
	cmd := &cobra.Command{
		Use:           "goose-guard --grace D --stderr-file F --arg A",
		Short:         "Guard a goose-watch wrapper (internal)",
		Hidden:        true,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if grace <= 0 || stderrFile == "" || arg == "" {
				return &exitError{Code: goosewatch.ExitUsage, Msg: "goose-guard: --grace, --stderr-file and --arg are required"}
			}
			in := os.NewFile(guardPipeFD, "guard-pipe")
			if in == nil {
				return &exitError{Code: goosewatch.ExitCannotRun, Msg: "goose-guard: no pipe on fd 3"}
			}
			code := goosewatch.RunGuard(goosewatch.GuardConfig{
				In:         in,
				Grace:      grace,
				StderrFile: stderrFile,
				Arg:        arg,
			})
			if code == 0 {
				return nil
			}
			return &exitError{Code: code, Msg: fmt.Sprintf("goose-guard: exit %d", code)}
		},
	}
	f := cmd.Flags()
	f.DurationVar(&grace, "grace", 0, "how long goose has after TERM before KILL")
	f.StringVar(&stderrFile, "stderr-file", "", "per-launch file the guard appends its one line to")
	f.StringVar(&arg, "arg", "", "one whole argument of goose's command line the proof matches")
	return cmd
}
