package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// rpcCall is how the verbs reach the daemon; tests replace it.
type rpcCall func(daemonrpc.Request) (*daemonrpc.Response, error)

const suspendLimits = `Limits in this release:
  - The processes are collected from one snapshot of the process table, plus one
    walk after the tmux server stops. A process forked after that walk keeps
    running while its seat is stopped.
  - Resume shifts each resumed seat's heartbeat by the length of the pause. It
    does not yet shift the other timers (activity stamps, backoff, health
    windows), so those read the pause as elapsed time.
  - Bus presence lapses during a suspend and returns shortly after resume.`

// suspendCmd is marvel suspend: freeze the tmux server and every seat process
// in place (docs/design/fleet-suspend-resume.md sections 2 and 4).
func suspendCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "suspend",
		Short: "Freeze every seat and the tmux server in place",
		Long: `Freeze every seat and the tmux server in place, so the fleet keeps its
context and its in-flight turns while nothing runs. The daemon keeps
answering; marvel status reports the suspend and marvel resume ends it.

On a terminal, suspend states how many seats it freezes and asks [y/N]. With no
terminal it refuses unless --yes is given.

` + suspendLimits + `

Exit codes: 0 suspended, 1 error.`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in := cmd.InOrStdin()
			f, ok := in.(*os.File)
			interactive := ok && term.IsTerminal(int(f.Fd()))
			code, err := runSuspend(cmd.OutOrStdout(), in, interactive, yes, send)
			return verbExit(code, err)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "suspend without asking")
	return cmd
}

// resumeCmd is marvel resume: continue a suspended cluster.
func resumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Resume a suspended cluster",
		Long: `Resume a suspended cluster: send SIGCONT to every recorded process, seats first
and the tmux server last, shift each resumed seat's heartbeat by the length of
the pause, and lift the daemon's hold. A daemon that started with the cluster
suspended then adopts the live panes in the background; marvel status shows it
until it finishes. There is no dialog.

A seat whose process had gone, or that still reads stopped, is listed and the
exit code is 3. Nothing is restarted.

` + suspendLimits + `

Exit codes: 0 resumed, 3 resumed with processes gone or still stopped, 1 error.`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			code, err := runResume(cmd.OutOrStdout(), send)
			return verbExit(code, err)
		},
	}
}

// verbExit turns a verb's code and error into what cobra returns, printing the
// error once.
func verbExit(code int, err error) error {
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
	}
	if code != 0 {
		msg := "failed"
		if err != nil {
			msg = err.Error()
		}
		return &exitError{Code: code, Msg: msg}
	}
	return nil
}

// runSuspend confirms, asks the daemon to suspend, and writes the outcome.
func runSuspend(out io.Writer, in io.Reader, interactive, yes bool, call rpcCall) (int, error) {
	if !yes {
		if !interactive {
			return 1, errors.New("suspend needs a terminal to confirm; run it in one, or pass --yes. Nothing was changed")
		}
		seats, err := seatCount(call)
		if err != nil {
			return 1, err
		}
		_, _ = fmt.Fprintf(out, "This freezes the tmux server and every seat process (%d seats); the daemon keeps answering and marvel resume continues them.\nSuspend the cluster? [y/N] ", seats)
		answer, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return 1, fmt.Errorf("read answer: %w", err)
		}
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			return 1, errors.New("not confirmed; nothing was changed")
		}
	}
	params, err := json.Marshal(map[string]int{"cli_pid": os.Getpid()})
	if err != nil {
		return 1, err
	}
	resp, err := call(daemonrpc.Request{Method: "suspend", Params: params})
	if err != nil {
		return 1, err
	}
	if err := verbRefusal(resp, "suspend"); err != nil {
		return 1, err
	}
	var res struct {
		Seats int   `json:"seats"`
		Gone  []int `json:"gone"`
	}
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		return 1, fmt.Errorf("decode suspend result: %w", err)
	}
	_, _ = fmt.Fprintf(out, "cluster: suspended (%d seat processes frozen)\n", res.Seats)
	if len(res.Gone) > 0 {
		_, _ = fmt.Fprintf(out, "exited while stopping: %s\n", pidList(res.Gone))
	}
	return 0, nil
}

// runResume asks the daemon to resume and writes the outcome.
func runResume(out io.Writer, call rpcCall) (int, error) {
	resp, err := call(daemonrpc.Request{Method: "resume"})
	if err != nil {
		return 1, err
	}
	if err := verbRefusal(resp, "resume"); err != nil {
		return 1, err
	}
	var res struct {
		Gone         []int `json:"gone"`
		StillStopped []int `json:"still_stopped"`
	}
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		return 1, fmt.Errorf("decode resume result: %w", err)
	}
	_, _ = fmt.Fprintln(out, "cluster: resumed")
	if len(res.Gone) == 0 && len(res.StillStopped) == 0 {
		return 0, nil
	}
	if len(res.Gone) > 0 {
		_, _ = fmt.Fprintf(out, "gone before resume (exited or replaced): %s\n", pidList(res.Gone))
	}
	if len(res.StillStopped) > 0 {
		_, _ = fmt.Fprintf(out, "still stopped after SIGCONT: %s\n", pidList(res.StillStopped))
	}
	return 3, nil
}

// verbRefusal returns the daemon's error for method, naming an old daemon
// that has no such method.
func verbRefusal(resp *daemonrpc.Response, method string) error {
	if resp.Error == "" {
		return nil
	}
	if strings.HasPrefix(resp.Error, "unknown method") {
		return fmt.Errorf("the daemon predates marvel %s; upgrade and restart it", method)
	}
	return errors.New(resp.Error)
}

// seatCount asks the daemon how many sessions it holds.
func seatCount(call rpcCall) (int, error) {
	params, err := json.Marshal(map[string]any{"resource_type": "sessions"})
	if err != nil {
		return 0, err
	}
	resp, err := call(daemonrpc.Request{Method: "get", Params: params})
	if err != nil {
		return 0, err
	}
	if resp.Error != "" {
		return 0, errors.New(resp.Error)
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(resp.Result, &rows); err != nil {
		return 0, fmt.Errorf("decode sessions: %w", err)
	}
	return len(rows), nil
}

func pidList(pids []int) string {
	parts := make([]string, len(pids))
	for i, p := range pids {
		parts[i] = fmt.Sprint(p)
	}
	return strings.Join(parts, ", ")
}
