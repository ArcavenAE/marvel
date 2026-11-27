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
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"

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
	// Mrvl says the verb runs the attach over the daemon's mrvl:// channel
	// for Desk. Argv then holds the printed ssh line, kept only as the
	// readable fallback against a daemon that lacks the channel.
	Mrvl bool
	Desk string
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
		return attachPlan{}, fmt.Errorf("desk %q is not a desk number: pass the DESK column of `marvel get sessions --columns name,desk`, for example 23", desk)
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
		argv := remoteSSHArgv(desk, tgt.Host, tgt.Socket)
		if strings.HasPrefix(tgt.Host, "-") || tgt.Host == "" {
			return attachPlan{}, fmt.Errorf("cannot build an ssh command for host %q", tgt.Host)
		}
		// --print shows the ssh form and runs nothing. Without it the verb
		// runs the attach over the mrvl:// channel, and keeps the ssh line
		// only as what it prints against a daemon too old to serve it.
		return attachPlan{Argv: argv, Mrvl: !printOnly, Desk: desk}, nil
	}

	pane := "%" + desk
	attach := []string{"tmux", "-L", tgt.Socket, "attach", "-t", pane}
	switch {
	case printOnly:
		return attachPlan{Argv: attach}, nil
	case insideTmuxServer(tmuxEnv, tgt.Socket):
		return attachPlan{Argv: []string{"tmux", "-L", tgt.Socket, "switch-client", "-t", pane}, Exec: true}, nil
	case tmuxEnv != "":
		// Inside another tmux server: open a window on the operator's server
		// that runs the attach to marvel's. The outer tmux finds its server
		// through $TMUX; the window's own client is a client of marvel's
		// server, which tmux does not treat as nesting because $TMUX names a
		// different server.
		return attachPlan{Argv: []string{"tmux", "new-window", "tmux", "-L", tgt.Socket, "attach", "-t", pane}, Exec: true}, nil
	}
	return attachPlan{Argv: attach, Exec: true}, nil
}

// checkNewWindowTarget asks marvel's tmux server whether the desk's pane still
// exists, before a window is opened for it. The window command exits 0 once
// the window exists, whatever the attach inside it does, so a pane that closed
// since the plan, or a server that is gone, would otherwise show the operator
// a window that closes at once. A pane id is resolved by has-session, which
// fails on a missing pane; display-message falls back to the current pane
// (from $TMUX_PANE) and exits 0. Plans that run in place are not checked,
// since the attach they run reports its own error.
func checkNewWindowTarget(plan attachPlan, socket, desk string) error {
	if len(plan.Argv) < 2 || plan.Argv[1] != "new-window" {
		return nil
	}
	out, err := exec.Command("tmux", "-L", socket, "has-session", "-t", "%"+desk).CombinedOutput()
	if err == nil {
		return nil
	}
	why := strings.TrimSpace(string(out))
	if why == "" {
		why = err.Error()
	}
	return fmt.Errorf("%s; to attach by hand: %s", why, quoteArgv(plan.Argv[2:]))
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

// remoteSSHArgv is the ssh command that opens a remote desk with the
// operator's own ssh access. It is what --print shows for a remote cluster,
// and what a daemon too old for the mrvl:// attach gets printed.
func remoteSSHArgv(desk, host, socket string) []string {
	return []string{"ssh", "-t", host, "tmux", "-L", socket, "attach", "-t", "%" + desk}
}

// runRemoteAttach runs a remote plan over the mrvl:// channel. A success
// writes nothing: the terminal was the output. A daemon without the channel
// gets the readable failure, the ssh line on stdout and a note naming the
// upgrade, with a nonzero exit. Any other failure is returned as it is, with
// no ssh line in its place, so a revoked key never reads as a working
// fallback.
func runRemoteAttach(w, stderr io.Writer, addr string, plan attachPlan, env attachEnv) error {
	if env.Mrvl == nil {
		return errors.New("this build cannot run a remote attach; use --print")
	}
	err := env.Mrvl(addr, plan.Desk)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, daemonrpc.ErrAttachUnsupported):
		// A failed write must not hide the error returned below.
		_, _ = fmt.Fprintln(w, quoteArgv(plan.Argv))
		_, _ = fmt.Fprintln(stderr, "marvel attach: the daemon at "+addr+" is too old to serve a remote attach; upgrade it, or run the ssh command above yourself")
		return fmt.Errorf("remote attach is not supported by the daemon at %s", addr)
	case errors.Is(err, daemonrpc.ErrLostContact):
		// One fixed line; the terminal is already restored. No ssh line: the
		// daemon answered a moment ago, so this is the link, not the version.
		var lc *daemonrpc.LostContactError
		reason := "no reply"
		if errors.As(err, &lc) {
			reason = lc.Error()
		}
		_, _ = fmt.Fprintf(stderr, "attach: lost contact with %s (%s)\n", addr, reason)
		return fmt.Errorf("lost contact with %s", addr)
	default:
		return fmt.Errorf("attach desk %s over %s: %w", plan.Desk, addr, err)
	}
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
	// Mrvl runs the attach for a desk over the mrvl:// channel at addr.
	Mrvl func(addr, desk string) error
	// Stderr is where the verb says which session a desk resolved to. Nil
	// means os.Stderr.
	Stderr io.Writer
}

func processAttachEnv() attachEnv {
	return attachEnv{Getenv: os.Getenv, Exec: execArgv, Mrvl: attachOverMrvl}
}

// attachOverMrvl gives the caller's terminal to a desk on a remote daemon: it
// puts stdin in raw mode, follows window-size changes, and restores the
// terminal when the attach ends.
func attachOverMrvl(addr, desk string) error {
	_, opts, err := resolveDaemonAddr()
	if err != nil {
		return err
	}
	fd := int(os.Stdin.Fd()) //nolint:gosec // a file descriptor number fits an int
	if !term.IsTerminal(fd) {
		return errors.New("marvel attach needs a terminal on stdin; use --print for the command")
	}
	cols, rows, err := term.GetSize(fd)
	if err != nil {
		return fmt.Errorf("read terminal size: %w", err)
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("set terminal raw: %w", err)
	}
	defer func() { _ = term.Restore(fd, state) }()

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	resize := make(chan daemonrpc.Size, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-winch:
				if c, r, err := term.GetSize(fd); err == nil {
					select {
					case resize <- daemonrpc.Size{Rows: r, Cols: c}:
					default:
					}
				}
			case <-done:
				return
			}
		}
	}()
	return daemonrpc.Attach(addr, desk, opts, daemonrpc.AttachIO{In: os.Stdin, Out: os.Stdout, Rows: rows, Cols: cols, Resize: resize})
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
func runAttach(w io.Writer, arg string, printOnly bool, env attachEnv) error {
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
	resolved, err := resolveAttachArg(arg, sessions)
	if err != nil {
		return err
	}
	plan, err := planAttach(resolved.Desk, sessions, attachTarget{Socket: st.TmuxSocket, Remote: remote, Host: host}, env.Getenv("TMUX"), printOnly)
	if err != nil {
		return err
	}
	// Said last, after every check has passed and before the command runs or is
	// printed, so the line never names a session that did not open.
	stderr := env.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	// A failed write to stderr must not stop the attach it only announces.
	_, _ = fmt.Fprintf(stderr, "%s is desk %s\n", resolved.Key, resolved.Desk)
	if plan.Mrvl {
		return runRemoteAttach(w, stderr, addr, plan, env)
	}
	if plan.Exec {
		if err := checkNewWindowTarget(plan, st.TmuxSocket, resolved.Desk); err != nil {
			return err
		}
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
		Use:   "attach <desk|key|name>",
		Short: "Open a session's tmux pane by desk number, session key or name",
		Long: `Open a session's tmux pane by desk number, session key or name.

The argument is one of three forms, resolved on the selected cluster
(--cluster) only:

  - a desk, the DESK column of ` + "`marvel get sessions --columns name,desk`" + ` (opt-in), with or without a leading
    percent sign (23 or %23);
  - a session key, workspace/name, matched exactly;
  - a bare session name, accepted only when exactly one session on the cluster
    has it. A name shared by several sessions is refused and every key is
    listed.

Digits that are both a desk and a session name are refused as ambiguous: pass
%23 for the desk or the key for the session. Every successful resolution
writes one line to stderr naming the session and its desk, so a reused desk
number shows. The verb reads the tmux socket from the daemon and attaches to
the pane.

  - Outside tmux it replaces itself with ` + "`tmux -L <socket> attach -t %<desk>`" + `.
  - Inside marvel's own tmux server it runs switch-client, so the client moves
    to the pane and nothing nests.
  - Inside any other tmux server it opens a new window on that server, running
    the attach to marvel's server.
  - --print prints the command and exits 0 in every case.

A desk on a remote cluster is run over the cluster's mrvl:// channel (port 6785)
with the key the cluster pins, which must be an admin key on that daemon. The
daemon refuses unless its own cluster entry sets allow_remote_attach: true, which
it reads at start, so a change needs a daemon restart. It needs no ssh login on
the remote host.

What a remote attach reaches: the session is a full tmux client of the daemon's
tmux server, and nothing unbinds its prefix or command prompt. With the gate on,
an admin key can open windows, run commands and switch to any session on that
server, so it is a shell as the daemon's user and not one desk. --print shows the ssh form instead,
` + "`ssh -t <host> tmux -L <socket> attach -t %<desk>`" + `, which uses your own ssh
access: the host is the one in the cluster's address, so it may need a login
name or an ssh config entry of your own. Against a daemon too old to serve the
channel, the verb prints that ssh line, says to upgrade the daemon, and exits
nonzero. A remote attach checks the link every 5 seconds; if the daemon stops
answering for 15 seconds (a closed lid, a dropped route), the verb prints
"attach: lost contact with <address> (no reply for 15s)" and exits nonzero.

A desk is unique only within one cluster's tmux server, so a desk number is
never looked up across clusters.

Leaving the session. Typing exit or Ctrl-D in its pane stops the agent; detaching
from tmux does not. Detach with Ctrl-B, then d. Ctrl-B is tmux's default prefix;
marvel does not change it, and a prefix set in your own tmux configuration
replaces it in the keys below. Where you land depends on how the session was
opened (measured on tmux 3.7b):

  - Outside tmux, and the watch's Enter outside tmux: Ctrl-B d detaches and
    returns you to the shell, or to the watch.
  - Inside marvel's own tmux server: Ctrl-B d detaches from tmux altogether and
    does not return you to the watch. Ctrl-B L switches back to the session you
    came from, and the agent keeps running.
  - Inside any other tmux server: the session is in a new window of that server,
    and Ctrl-B goes to your own tmux. Ctrl-B p moves to the previous window and
    leaves the session attached. Ctrl-B Ctrl-B d detaches the session and closes
    its window. Ctrl-B d detaches your own tmux, not the session.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAttach(cmd.OutOrStdout(), args[0], printOnly, processAttachEnv())
		},
	}
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the command and exit 0 instead of running it")
	return cmd
}
