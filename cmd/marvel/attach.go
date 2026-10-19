package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemon"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// attachTarget is what the plan needs to know about the daemon it resolved
// the desk against: the tmux socket the daemon reported, and for a remote
// daemon the host an operator would ssh to.
type attachTarget struct {
	Socket string
	Remote bool
	Host   string
}

// attachPlan is what `marvel attach` will do for one desk. Argv is the
// command. Exec says the verb runs it in place of itself; otherwise the verb
// prints it. Note says why a command that could have run is only printed.
type attachPlan struct {
	Argv []string
	Exec bool
	Note string
}

// deskArg is a desk as typed: the digits of a tmux pane id, with or without
// the leading percent sign the pane id carries. Nothing else reaches a
// command line.
var deskArg = regexp.MustCompile(`^%?([0-9]+)$`)

// planAttach turns a desk into the command that opens it. It is a pure
// function of the session rows, the daemon's own report and $TMUX, so the
// branches test without a tmux server.
//
// A desk is the pane id of a session row without its percent sign, unique
// per tmux server. The verb resolves it against the rows of the one cluster
// it was pointed at, so a desk number never resolves across clusters.
func planAttach(desk string, sessions []api.Session, tgt attachTarget, tmuxEnv string, printOnly bool) (attachPlan, error) {
	m := deskArg.FindStringSubmatch(desk)
	if m == nil {
		return attachPlan{}, fmt.Errorf("desk %q is not a desk number: pass the DESK column of `marvel get sessions`, for example 23", desk)
	}
	desk = m[1]

	matches := 0
	for _, s := range sessions {
		if strings.TrimPrefix(s.PaneID, "%") == desk {
			matches++
		}
	}
	switch {
	case matches == 0:
		return attachPlan{}, fmt.Errorf("no session has desk %s on this cluster", desk)
	case matches > 1:
		return attachPlan{}, fmt.Errorf("desk %s names more than one session row on this cluster", desk)
	}

	if tgt.Socket == "" {
		return attachPlan{}, errors.New("this daemon does not report its tmux socket; upgrade it (marvel#914), then retry")
	}

	if tgt.Remote {
		argv, printOnly := remoteAttach(desk, tgt.Host, tgt.Socket)
		if strings.HasPrefix(tgt.Host, "-") || tgt.Host == "" {
			return attachPlan{}, fmt.Errorf("cannot build an ssh command for host %q", tgt.Host)
		}
		return attachPlan{Argv: argv, Exec: !printOnly}, nil
	}

	pane := "%" + desk
	attach := []string{"tmux", "-L", tgt.Socket, "attach", "-t", pane}
	switch {
	case printOnly:
		return attachPlan{Argv: attach}, nil
	case insideTmuxServer(tmuxEnv, tgt.Socket):
		return attachPlan{Argv: []string{"tmux", "-L", tgt.Socket, "switch-client", "-t", pane}, Exec: true}, nil
	case tmuxEnv != "":
		return attachPlan{
			Argv: attach,
			Note: "this shell is inside a tmux server that is not marvel's, and attaching from here would nest one tmux in another; run the command from a shell outside tmux",
		}, nil
	}
	return attachPlan{Argv: attach, Exec: true}, nil
}

// insideTmuxServer reports whether $TMUX names the tmux server with this
// socket name. $TMUX is "<socket path>,<server pid>,<session id>"; the
// socket's name is the last element of that path.
func insideTmuxServer(tmuxEnv, socket string) bool {
	if tmuxEnv == "" || socket == "" {
		return false
	}
	path, _, _ := strings.Cut(tmuxEnv, ",")
	return filepath.Base(path) == socket
}

// remoteAttach is the whole of the verb's remote branch. Today it returns
// the ssh command and says it is print-only: marvel's mrvl:// listener
// carries RPC and no terminal, so there is nothing for it to run. A later
// integration that hands the terminal to switchboard replaces this body and
// nothing around it.
func remoteAttach(desk, host, socket string) (argv []string, printOnly bool) {
	return []string{"ssh", "-t", host, "tmux", "-L", socket, "attach", "-t", "%" + desk}, true
}

// quoteArgv renders argv as one shell line a person can paste. A word of
// plain characters stays bare, so the common command reads as typed.
func quoteArgv(argv []string) string {
	plain := regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)
	words := make([]string, len(argv))
	for i, a := range argv {
		if plain.MatchString(a) {
			words[i] = a
			continue
		}
		words[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(words, " ")
}

// attachEnv is what the verb takes from its process: the environment and the
// way to replace itself with a command. Both are fields so a test never
// execs.
type attachEnv struct {
	Getenv func(string) string
	Exec   func(argv []string) error
}

func processAttachEnv() attachEnv {
	return attachEnv{Getenv: os.Getenv, Exec: execArgv}
}

// execArgv replaces this process with argv, so the terminal belongs to tmux
// and no marvel process lingers behind it.
func execArgv(argv []string) error {
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	return syscall.Exec(path, argv, os.Environ())
}

// attachHost reads the host out of the address the client resolved, and
// reports whether the daemon is somewhere other than this machine's own unix
// socket. The port is dropped on purpose: it is the mrvl:// port, not an
// ssh one.
func attachHost(addr string) (host string, remote bool) {
	switch {
	case strings.Contains(addr, "://"):
		u, err := url.Parse(addr)
		if err != nil {
			return "", true
		}
		return u.Hostname(), true
	case strings.HasPrefix(addr, "/"):
		return "", false
	}
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h, true
	}
	return "", false
}

// fetchDaemonStatus asks the resolved daemon who it is.
func fetchDaemonStatus() (daemon.DaemonStatus, error) {
	var st daemon.DaemonStatus
	resp, err := send(daemonrpc.Request{Method: "daemon.status"})
	if err != nil {
		return st, err
	}
	if resp.Error != "" {
		return st, fmt.Errorf("%s", resp.Error)
	}
	if err := json.Unmarshal(resp.Result, &st); err != nil {
		return st, fmt.Errorf("decode daemon status: %w", err)
	}
	return st, nil
}

// runAttach resolves the desk against the selected cluster and prints or
// runs the command. It only reads from the daemon.
func runAttach(w io.Writer, desk string, printOnly bool, env attachEnv) error {
	addr, _, _, err := resolveDaemonRung()
	if err != nil {
		return err
	}
	sessions, err := fetchSessions()
	if err != nil {
		return err
	}
	st, err := fetchDaemonStatus()
	if err != nil {
		return err
	}
	host, remote := attachHost(addr)
	plan, err := planAttach(desk, sessions, attachTarget{Socket: st.TmuxSocket, Remote: remote, Host: host}, env.Getenv("TMUX"), printOnly)
	if err != nil {
		return err
	}
	if plan.Exec {
		return env.Exec(plan.Argv)
	}
	if plan.Note != "" {
		fmt.Fprintln(os.Stderr, "marvel attach: "+plan.Note)
	}
	_, err = fmt.Fprintln(w, quoteArgv(plan.Argv))
	return err
}

func attachCmd() *cobra.Command {
	var printOnly bool
	cmd := &cobra.Command{
		Use:   "attach <desk>",
		Short: "Open a session's tmux pane by its desk number",
		Long: `Open a session's tmux pane by its desk number.

<desk> is the DESK column of ` + "`marvel get sessions`" + `. The verb finds that row on the
selected cluster (--cluster), reads the tmux socket from the daemon, and
attaches to the pane.

  - Outside tmux it replaces itself with ` + "`tmux -L <socket> attach -t %<desk>`" + `.
  - Inside marvel's own tmux server it runs switch-client, so the client moves
    to the pane and nothing nests.
  - Inside any other tmux server it prints the attach command and does not run
    it: attaching from there would nest one tmux in another.
  - --print prints the command and exits 0 in every case.

A desk on a remote cluster is always printed, never run:
` + "`ssh -t <host> tmux -L <socket> attach -t %<desk>`" + `. marvel adds no remote
terminal; the host is the one in the cluster's address, so it may need a login
name or an ssh config entry of your own.

A desk is unique only within one cluster's tmux server, so a desk number is
never looked up across clusters.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAttach(cmd.OutOrStdout(), args[0], printOnly, processAttachEnv())
		},
	}
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the command and exit 0 instead of running it")
	return cmd
}
