package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// Exit codes of `marvel wait`. A refusal or a bad argument is not one of them:
// those are an ordinary error and exit 1 through main.
const (
	waitSucceeded = 0
	waitFailed    = 1
	waitCrashed   = 2
	waitTimeout   = 3
	waitUnknown   = 4
	// waitDaemonError is a daemon the client cannot reach or one that answers
	// with an error. It says nothing about the session, so it is not waitFailed.
	waitDaemonError = 5
)

// exitError carries a process exit code out of a command. main exits with
// Code; Msg is what the command prints first. Err, when set, is the error
// behind it, so a typed connection error inside still reads as one: the
// usage block stays off, and Code still wins (exitCodeFor checks exitError
// first).
type exitError struct {
	Code int
	Msg  string
	Err  error
}

func (e *exitError) Error() string { return fmt.Sprintf("%s (exit %d)", e.Msg, e.Code) }
func (e *exitError) Unwrap() error { return e.Err }

// requireExperimental refuses a surface that is gated behind --experimental.
func requireExperimental(on bool, what string) error {
	if on {
		return nil
	}
	return fmt.Errorf("%s is experimental; pass --experimental to use it", what)
}

// sessionState reads one session's stored state through describe. A key the
// store does not hold is reported as found=false.
func sessionState(key string) (state api.SessionState, found bool, err error) {
	params, _ := json.Marshal(map[string]string{"resource_type": "session", "name": key})
	resp, err := send(daemonrpc.Request{Method: "describe", Params: params})
	if err != nil {
		return "", false, err
	}
	if resp.Error != "" {
		if strings.Contains(resp.Error, api.ErrNotFound.Error()) {
			return "", false, nil
		}
		return "", false, errors.New(resp.Error)
	}
	var sess api.Session
	if err := json.Unmarshal(resp.Result, &sess); err != nil {
		return "", false, fmt.Errorf("read session %s: %w", key, err)
	}
	return sess.State, true, nil
}

// waitForSession polls the stored state until it is terminal or the timeout
// passes. It reads the store, never the event ring, which drops events for a
// slow subscriber.
func waitForSession(key string, timeout, poll time.Duration) (string, int, error) {
	deadline := time.Now().Add(timeout)
	for {
		state, found, err := sessionState(key)
		if err != nil {
			return "", 0, err
		}
		if !found {
			return "unknown", waitUnknown, nil
		}
		switch state {
		case api.SessionSucceeded:
			return string(state), waitSucceeded, nil
		case api.SessionFailed:
			return string(state), waitFailed, nil
		case api.SessionCrashed:
			return string(state), waitCrashed, nil
		}
		if !time.Now().Add(poll).Before(deadline) {
			return string(state), waitTimeout, nil
		}
		time.Sleep(poll)
	}
}

func waitCmd() *cobra.Command {
	var timeout, poll time.Duration
	var experimental bool
	cmd := &cobra.Command{
		Use:   "wait <session-key>",
		Short: "Wait for a session to finish (experimental)",
		Long: `Wait for a session to reach a terminal state, reading its stored state.

Exit codes: 0 succeeded, 1 failed, 2 crashed, 3 timeout (still running),
4 unknown key, 5 the daemon could not be reached or answered with an error
(the session's state is not known; a caller should treat it as still pending).
A refused flag or a bad argument exits 1.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireExperimental(experimental, "marvel wait"); err != nil {
				return err
			}
			if poll <= 0 {
				return fmt.Errorf("--poll must be positive")
			}
			state, code, err := waitForSession(args[0], timeout, poll)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "unavailable")
				return &exitError{Code: waitDaemonError, Msg: fmt.Sprintf("session/%s: %v", args[0], err), Err: err}
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), state)
			if code == waitSucceeded {
				return nil
			}
			return &exitError{Code: code, Msg: fmt.Sprintf("session/%s %s", args[0], state)}
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "give up after this long and exit 3")
	cmd.Flags().DurationVar(&poll, "poll", 5*time.Second, "how often to read the session state")
	cmd.Flags().BoolVar(&experimental, "experimental", false, "allow this experimental command")
	return cmd
}
