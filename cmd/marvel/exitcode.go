package main

import (
	"errors"
	"io"

	"github.com/spf13/cobra"

	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/knownhosts"
)

// Exit codes for a failure to reach or use a daemon. 0 and 1 keep their
// meaning (success; a command error), and 2 to 4 belong to marvel wait
// (wait.go). 5 is an answer that could not be used; inside wait it keeps
// its wider meaning, the daemon failing the call.
const (
	exitBadReply     = 5
	exitUnreachable  = 6
	exitTimeout      = 7
	exitAuthRefused  = 8
	exitHostKeyError = 9
)

// exitCodeFor maps an error to the process exit code. An exitError carries
// its own code; a typed connection error maps by its type, found through
// any wrapping; anything else is 1.
func exitCodeFor(err error) int {
	if err == nil {
		return 0
	}
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	if code, ok := connectExitCode(err); ok {
		return code
	}
	return 1
}

// connectExitCode reports the exit code for a typed connection error, and
// false for any other error.
func connectExitCode(err error) (int, bool) {
	var (
		bad  *daemonrpc.BadReplyError
		unr  *daemonrpc.UnreachableError
		tmo  *daemonrpc.TimeoutError
		ref  *daemonrpc.AuthRefusedError
		noID *daemonrpc.NoIdentityError
		unk  *knownhosts.HostKeyUnknownError
		chg  *knownhosts.HostKeyChangedError
	)
	switch {
	case errors.As(err, &unk), errors.As(err, &chg):
		return exitHostKeyError, true
	case errors.As(err, &ref), errors.As(err, &noID):
		return exitAuthRefused, true
	case errors.As(err, &tmo):
		return exitTimeout, true
	case errors.As(err, &unr):
		return exitUnreachable, true
	case errors.As(err, &bad):
		return exitBadReply, true
	}
	return 0, false
}

// silenceUsageOnConnectErrors wraps the RunE of every command in the tree
// so that a typed connection error sets SilenceUsage before cobra prints:
// a daemon that could not be reached or used is not a mistake in how the
// command was typed, so it gets its one error line and no usage block.
// Every other error prints as before.
func silenceUsageOnConnectErrors(cmd *cobra.Command) {
	if run := cmd.RunE; run != nil {
		cmd.RunE = func(c *cobra.Command, args []string) error {
			err := run(c, args)
			if _, typed := connectExitCode(err); typed {
				c.SilenceUsage = true
			}
			return err
		}
	}
	for _, sub := range cmd.Commands() {
		silenceUsageOnConnectErrors(sub)
	}
}

// execute runs the command tree with its errors going to stderr and
// returns the process exit code.
func execute(root *cobra.Command, stderr io.Writer) int {
	root.SetErr(stderr)
	return exitCodeFor(root.Execute())
}
