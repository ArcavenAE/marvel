package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"github.com/arcavenae/marvel/internal/admission"
	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemon"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/keys"
	"github.com/arcavenae/marvel/internal/paths"
	"github.com/arcavenae/marvel/internal/rlog"
	"github.com/arcavenae/marvel/internal/team"
	"github.com/arcavenae/marvel/internal/tmux"
	"github.com/arcavenae/marvel/internal/upgrade"
	"github.com/spf13/cobra"
)

// Set by -ldflags at build time.
var (
	version = "dev"
	channel = "dev"
)

var (
	clusterName  string // --cluster flag
	socketPath   string // --socket flag (fallback)
	identityPath string // --identity flag (per-invocation override)
)

// resolveDaemon returns both the address and the dial options for the
// selected cluster. --identity overrides the cluster-level identity.
// Precedence: --socket, then MARVEL_SOCKET, then the selected cluster's
// Socket or Server, then the layout default (~/.marvel/run/marvel.sock).
// config.ResolveSocket covers the last two rungs so every fall-through
// branch below lands on the same answer; four of them used to reach a
// hardcoded machine-global path instead. See
// docs/design/daemon-isolation.md decision 3.
func resolveDaemon() (string, daemon.DialOptions) {
	addr, opts := resolveDaemonAddr()
	if w := config.LegacySocketWarning(addr); w != "" {
		fmt.Fprintln(os.Stderr, w)
	}
	return addr, opts
}

func resolveDaemonAddr() (string, daemon.DialOptions) {
	if socketPath != "" {
		return socketPath, daemon.DialOptions{Identity: identityPath}
	}
	if env := os.Getenv(config.SocketEnv); env != "" {
		return env, daemon.DialOptions{Identity: identityPath}
	}
	cfg, err := config.Load()
	if err != nil {
		return config.ResolveSocket(), daemon.DialOptions{Identity: identityPath}
	}
	cl, err := cfg.GetCluster(clusterName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
		return config.ResolveSocket(), daemon.DialOptions{Identity: identityPath}
	}
	if cl == nil {
		return config.ResolveSocket(), daemon.DialOptions{Identity: identityPath}
	}
	addr := cl.Socket
	if cl.Server != "" {
		addr = cl.Server
	}
	if addr == "" {
		addr = config.ResolveSocket()
	}
	id := identityPath
	if id == "" {
		id = cl.Identity
	}
	return addr, daemon.DialOptions{Identity: id}
}

// send runs a JSON-RPC request against the currently selected daemon,
// threading through per-cluster dial options, and warns when the daemon
// that answered is not rooted at the layout home this client resolved
// against. All subcommands should use this instead of
// daemon.SendRequest directly.
func send(req daemon.Request) (*daemon.Response, error) {
	addr, opts := resolveDaemon()
	resp, err := daemon.SendRequestWith(addr, req, opts)
	if err != nil {
		return nil, err
	}
	// Diagnostic only, and deliberately after the fact: the daemon's
	// self-reported home arrives on the response, so a mutating request
	// has already been carried out by the time a mismatch is visible.
	// Prevention would put the expectation on the request and have the
	// daemon reject it, which is authorization-shaped (aae-orc-sqh0).
	// See docs/design/daemon-isolation.md decision 8.
	if w := config.DaemonHomeWarning(addr, resp.DaemonHome, config.ClientHome()); w != "" {
		fmt.Fprintln(os.Stderr, w)
	}
	return resp, nil
}

func main() {
	// Strip shell-style comments from args so inline notes work:
	//   ./marvel shift test/squad  # replace all workers
	os.Args = stripComments(os.Args)

	root := &cobra.Command{
		Use:   "marvel",
		Short: "Agent orchestration control plane",
	}

	root.PersistentFlags().StringVar(&clusterName, "cluster", "",
		"named cluster from ~/.marvel/config.yaml")
	root.PersistentFlags().StringVar(&socketPath, "socket", "",
		"explicit daemon address (overrides --cluster)")
	root.PersistentFlags().StringVarP(&identityPath, "identity", "i", "",
		"private key file for SSH auth (overrides cluster identity)")

	root.AddCommand(daemonCmd())
	root.AddCommand(workCmd())
	root.AddCommand(getCmd())
	root.AddCommand(describeCmd())
	root.AddCommand(deleteCmd())
	root.AddCommand(scaleCmd())
	root.AddCommand(convergeCmd())
	root.AddCommand(runCmd())
	root.AddCommand(killCmd())
	root.AddCommand(shiftCmd())
	root.AddCommand(resetHealthCmd())
	root.AddCommand(injectCmd())
	root.AddCommand(captureCmd())
	root.AddCommand(versionCmd())
	root.AddCommand(upgradeCmd())
	root.AddCommand(keysCmd())
	root.AddCommand(configCmd())
	root.AddCommand(stopCmd())
	root.AddCommand(eventsCmd())
	root.AddCommand(reapCmd())
	root.AddCommand(orphansCmd())
	root.AddCommand(newCtxForwardCmd())
	root.AddCommand(newCodexCtxCmd())
	root.AddCommand(planCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// shiftTimeoutEnv is the environment variable that seeds the daemon's
// shift timeout when --shift-timeout is not passed. See aae-orc-sape.
const shiftTimeoutEnv = "MARVEL_SHIFT_TIMEOUT"

// resolveShiftTimeout picks the effective shift timeout for the daemon.
// The --shift-timeout flag wins when the operator set it; otherwise the
// MARVEL_SHIFT_TIMEOUT env var is parsed as a Go duration; otherwise zero,
// which leaves the team controller on its built-in 10-minute default.
func resolveShiftTimeout(flagVal time.Duration, flagChanged bool, env string) (time.Duration, error) {
	if flagChanged {
		return flagVal, nil
	}
	if env == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(env)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", shiftTimeoutEnv, env, err)
	}
	return d, nil
}

func daemonCmd() *cobra.Command {
	var mrvlAddr string
	var listenSocket string
	var logFilePath string
	var pidFilePath string
	var logMaxSizeMiB int
	var logMaxFiles int
	var logMaxTotalMiB int
	var stateBoltPath string
	var shiftTimeout time.Duration
	var reclaim bool

	layout, _ := paths.Default()
	defaultLog := ""
	defaultPid := ""
	defaultBolt := ""
	if layout.Home != "" {
		defaultLog = layout.DaemonLog()
		defaultPid = layout.DaemonPid()
		defaultBolt = layout.DaemonBolt()
	}

	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Start the marvel daemon",
		Long: `Start the marvel daemon. Listens on a Unix socket for local access.
Use --mrvl to also start the mrvl:// listener for remote access.

Examples:
  marvel daemon                              # Unix socket only
  marvel daemon --mrvl                       # + mrvl:// on port 6785
  marvel daemon --mrvl=:7000                 # + mrvl:// on custom port
  marvel daemon --socket /var/marvel.sock    # custom socket path
  marvel daemon --log-file= --pidfile=       # disable log tee and pidfile
  marvel daemon --state-bolt=                # disable L2 persistence (in-memory only; daemon restart kills agents)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sock := listenSocket
			if sock == "" {
				sock = config.ResolveSocket()
			}
			if w := config.LegacySocketWarning(sock); w != "" {
				fmt.Fprintln(os.Stderr, w)
			}
			if err := paths.CheckSocketPath(sock); err != nil {
				return err
			}
			// net.Listen needs run/ to exist, and it is the directory
			// mode (0700) that protects the socket: nothing chmods the
			// socket itself, so with umask 022 it is created 0755.
			// Scoped to the layout's own run dir, so an operator-supplied
			// --socket elsewhere is never silently mkdir'd.
			if layout.Home != "" && filepath.Dir(sock) == layout.RunDir() {
				if err := layout.EnsureRunDir(); err != nil {
					return err
				}
			}

			// Ensure ~/.marvel/state/ exists before OpenBolt would try
			// to write to it. bbolt requires the parent directory.
			if stateBoltPath != "" && layout.Home != "" {
				if err := layout.EnsureStateDir(); err != nil {
					return err
				}
			}

			shiftTO, err := resolveShiftTimeout(
				shiftTimeout, cmd.Flags().Changed("shift-timeout"), os.Getenv(shiftTimeoutEnv),
			)
			if err != nil {
				return err
			}

			d, err := daemon.NewWithOptions(daemon.Options{
				PidFile:      pidFilePath,
				StateBolt:    stateBoltPath,
				ShiftTimeout: shiftTO,
				Reclaim:      reclaim,
			})
			if err != nil {
				return err
			}

			// Tee Go's log output into: stderr (only when interactive)
			// + the in-memory ring + optional log file.
			//
			// Including stderr unconditionally caused duplicates under
			// the common nohup pattern: `nohup marvel daemon --log-file
			// X >X 2>&1` — stderr gets redirected to the same file
			// that the MultiWriter is already writing to, so each line
			// landed twice (reported as ArcavenAE/marvel#12 by Skippy).
			// Now: tee stderr only when it is a terminal, so background
			// daemons write exactly once to the log file and interactive
			// runs still see their own output in the console.
			writers := []io.Writer{d.LogBuffer()}
			if term.IsTerminal(int(os.Stderr.Fd())) {
				writers = append(writers, os.Stderr)
			}
			var logCloser io.Closer
			if logFilePath != "" {
				closer, err := openRotatingLog(logFilePath, logMaxSizeMiB, logMaxFiles, logMaxTotalMiB)
				if err != nil {
					return err
				}
				logCloser = closer
				defer func() { _ = logCloser.Close() }()
				if w, ok := closer.(io.Writer); ok {
					writers = append(writers, w)
				}
			}
			log.SetOutput(io.MultiWriter(writers...))
			if logFilePath != "" {
				log.Printf("daemon log file: %s", logFilePath)
			}

			if err := d.Start(sock); err != nil {
				return err
			}
			if pidFilePath != "" {
				log.Printf("daemon pidfile: %s (pid %d)", pidFilePath, os.Getpid())
			}

			if cmd.Flags().Changed("mrvl") {
				if err := d.StartMRVL(mrvlAddr); err != nil {
					return err
				}
			}

			// Wait for signal. A signal detaches: agents keep running
			// and the next start adopts their panes. Tearing them down
			// is an explicit operator act (`marvel stop --teardown`),
			// not something a package upgrade or a stray Ctrl-C does.
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
			<-sig
			fmt.Println("\ndetaching, agents keep running (marvel stop --teardown to end them)")
			d.Detach()
			return nil
		},
	}
	// --mrvl is optional-valued: bare --mrvl enables on the default port,
	// --mrvl=:7000 enables on a custom port. NoOptDefVal prevents cobra
	// from eating the next token when the flag is supplied without =.
	f := cmd.Flags().VarPF(newOptionalString(&mrvlAddr), "mrvl", "",
		"start mrvl:// listener (use --mrvl=:<port> for a custom port)")
	f.NoOptDefVal = ":" + config.DefaultMRVLPort
	cmd.Flags().StringVar(&listenSocket, "socket", "",
		"Unix socket path (default "+config.DefaultSocket()+", or $"+config.SocketEnv+")")
	cmd.Flags().BoolVar(&reclaim, "reclaim", false,
		"destroy marvel tmux state this daemon does not own, instead of leaving it running")
	cmd.Flags().StringVar(&logFilePath, "log-file", defaultLog,
		"tee daemon stderr to this file (empty string disables)")
	cmd.Flags().StringVar(&pidFilePath, "pidfile", defaultPid,
		"write pid to this file on start, remove on stop (empty string disables)")
	cmd.Flags().StringVar(&stateBoltPath, "state-bolt", defaultBolt,
		"bbolt L2 file for durable state (empty string disables persistence; daemon restart loses state and kills running agents per pre-L2 contract C12)")
	cmd.Flags().DurationVar(&shiftTimeout, "shift-timeout", 0,
		"abort and roll back a shift that has not reached readiness within this Go duration (0 uses the built-in 10m default; env "+shiftTimeoutEnv+" is the fallback when this flag is unset)")
	// Log rotation / retention — bounds disk usage for --log-file.
	// Motivated by desk Pi headroom (aae-orc-k0t, Skippy session-025/026).
	// Zero for any of these disables the corresponding limit.
	cmd.Flags().IntVar(&logMaxSizeMiB, "log-max-size", 10,
		"rotate --log-file when it exceeds this size in MiB (0 disables rotation)")
	cmd.Flags().IntVar(&logMaxFiles, "log-max-files", 5,
		"keep at most N gzipped archives of --log-file (0 keeps all)")
	cmd.Flags().IntVar(&logMaxTotalMiB, "log-max-total", 0,
		"cap total disk usage across --log-file and archives in MiB (0 disables)")

	cmd.AddCommand(daemonLogsCmd())
	cmd.AddCommand(daemonReexecCmd())
	return cmd
}

// daemonReexecCmd tells the running daemon to re-exec its own process
// image in place, adopting the live panes so agents keep running. This
// is the "self-update via exec" step: it adopts a binary that is already
// installed on disk. It is distinct from `marvel upgrade` (which fetches
// and installs a new binary) so the two compose rather than collide;
// `marvel upgrade --daemon` runs both in sequence.
func daemonReexecCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reexec",
		Short: "Re-exec the running daemon in place to adopt a freshly installed binary",
		Long: `Re-exec the running daemon in place.

The daemon checkpoints its state, releases its state file, and replaces
its own process image (same PID) with a fresh exec of the marvel binary
at its current path. Every agent keeps running in its tmux pane; the new
process re-opens the same state file, re-binds the same socket, and
adopts those panes.

Use this after installing a new binary out of band. To fetch, install,
and adopt in one step, use 'marvel upgrade --daemon'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := send(daemon.Request{Method: "reexec"})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			fmt.Println("marvel daemon re-executing in place; agents keep running")
			return nil
		},
	}
}

// daemonLogsCmd — fetch the daemon's recent log lines over mrvl://.
// Runs against any cluster; no SSH to the daemon host required.
func daemonLogsCmd() *cobra.Command {
	var n int
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Print the daemon's recent log lines (over mrvl:// if --cluster is set)",
		Long: `Fetch the daemon's in-memory log ring and print the most recent N lines.

The ring is populated by every 'log.Printf' the daemon emits — RPC
dispatch, session creation, health-check decisions, shifts, auth
events. Lines survive as long as the daemon process does; the ring
is bounded so memory stays flat.

Examples:
  marvel daemon logs                      # local daemon, last 100 lines
  marvel daemon logs -n 500               # last 500 lines
  marvel --cluster desk daemon logs       # remote daemon via mrvl://`,
		RunE: func(cmd *cobra.Command, args []string) error {
			params, _ := json.Marshal(map[string]int{"n": n})
			resp, err := send(daemon.Request{Method: "logs", Params: params})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			var result struct {
				Lines []string `json:"lines"`
			}
			if err := json.Unmarshal(resp.Result, &result); err != nil {
				return fmt.Errorf("parse logs: %w", err)
			}
			for _, line := range result.Lines {
				fmt.Println(line)
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&n, "lines", "n", 100, "number of recent lines to return (0 = all buffered)")
	return cmd
}

// eventsCmd prints the daemon's structured event ring — the
// marvel-native equivalent of `kubectl get events`. Complements
// `marvel daemon logs` (raw stderr stream) with queryable,
// severity-tagged history scoped to workspaces, teams, roles, and
// sessions.
func eventsCmd() *cobra.Command {
	var n int
	var workspace, team, role, session, kind string
	var warningsOnly bool
	var follow bool
	var listKinds bool
	cmd := &cobra.Command{
		Use:   "events",
		Short: "List recent session/team state-transition events",
		Long: `Fetch the daemon's structured event ring and print matching events.

Two families share the ring. Control-plane events report what marvel
did to a session: session.created / deleted / crashed from
session.Manager, and restart, crashloop-backoff, saturation, shift and
health events from team.Controller. Agent events (agent.*) report what
the agent inside a session did: session start and end with cost and
timing, messages, tool calls and results, permission prompts. Agent
events only appear for sessions whose runtime marvel can observe (a
headless stream-json launch today; see examples/claude-headless.toml).

Each event has a timestamp, kind, severity (info or warning), and
session coordinates.

Examples:
  marvel events                              # last 100 events
  marvel events -n 500                       # last 500 events
  marvel events --workspace demo             # filter by workspace
  marvel events --session util/shell-g1-0    # filter by session key
  marvel events --kind session.crashed       # only crashes
  marvel events --kind agent.tool.call       # what the agents are doing
  marvel events --kind agent.session.ended   # per-session cost and timing
  marvel events --kind context.limit-unresolved  # why a CTX% cell is blank
  marvel events --kind admission.refused     # spawns a team budget refused
  marvel events --warnings                   # only warning-severity events
  marvel events --follow                     # live tail; poll the ring every second
  marvel events --list-kinds                 # every kind --kind accepts
  marvel --cluster desk events               # remote daemon via mrvl://

A --kind that matches nothing prints no events rather than an error, so a
misspelled kind and a kind that never fired look the same. --list-kinds
prints the catalog, and needs no daemon.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if listKinds {
				printKindCatalog(os.Stdout)
				return nil
			}
			buildParams := func(sinceSeq uint64) json.RawMessage {
				params := map[string]any{"n": n}
				if sinceSeq > 0 {
					// Cursor requests return every new event, not a tail.
					params["n"] = 0
					params["since_seq"] = sinceSeq
				}
				if workspace != "" {
					params["workspace"] = workspace
				}
				if team != "" {
					params["team"] = team
				}
				if role != "" {
					params["role"] = role
				}
				if session != "" {
					params["session"] = session
				}
				if kind != "" {
					params["kind"] = kind
				}
				if warningsOnly {
					params["min_severity"] = "warning"
				}
				raw, _ := json.Marshal(params)
				return raw
			}
			fetch := func(sinceSeq uint64) ([]events.Event, error) {
				resp, err := send(daemon.Request{Method: "events", Params: buildParams(sinceSeq)})
				if err != nil {
					return nil, err
				}
				if resp.Error != "" {
					return nil, fmt.Errorf("%s", resp.Error)
				}
				var result struct {
					Events []events.Event `json:"events"`
				}
				if err := json.Unmarshal(resp.Result, &result); err != nil {
					return nil, fmt.Errorf("parse events: %w", err)
				}
				return result.Events, nil
			}
			printBatch := func(evs []events.Event, header bool) {
				tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				if header {
					_, _ = fmt.Fprintln(tw, "TIME\tSEV\tKIND\tSESSION\tMESSAGE")
				}
				for _, ev := range evs {
					sev := string(ev.Severity)
					if sev == "" {
						sev = "info"
					}
					sessRef := ev.Session
					if sessRef == "" && ev.Team != "" {
						sessRef = ev.Workspace + "/" + ev.Team
					}
					if sessRef == "" {
						sessRef = ev.Workspace
					}
					// Actor rides in the message rather than its own
					// column: it is set on a small minority of events,
					// and a column sized to a "pid=N socket=PATH" string
					// would push MESSAGE off the right of every row that
					// does not carry one.
					msg := ev.Message
					if ev.Actor != "" {
						msg = fmt.Sprintf("%s [by %s]", msg, ev.Actor)
					}
					_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
						ev.Timestamp.Format("15:04:05"), sev, ev.Kind, sessRef, msg)
				}
				_ = tw.Flush()
			}
			maxSeq := func(evs []events.Event, cur uint64) uint64 {
				for _, ev := range evs {
					if ev.Seq > cur {
						cur = ev.Seq
					}
				}
				return cur
			}

			evs, err := fetch(0)
			if err != nil {
				return err
			}
			// An empty result under a --kind nobody declares is the
			// silent-success shape: it reads as "this never happened".
			// Say which of the two it is, on stderr so a script's
			// stdout and exit code are unchanged.
			if len(evs) == 0 && kind != "" && !events.IsKnownKind(kind) {
				_, _ = fmt.Fprintf(os.Stderr, "note: no event kind named %q; run 'marvel events --list-kinds' for the catalog\n", kind)
			}
			if len(evs) == 0 && !follow {
				fmt.Println("no events")
				return nil
			}
			printBatch(evs, true)
			if !follow {
				return nil
			}
			// Follow mode: poll the ring with a Seq cursor so each event
			// prints exactly once, in order, until interrupted. The ring
			// assigns Seq monotonically, so a cursor survives ring
			// wraparound (missed events are simply gone, never repeated).
			cursor := maxSeq(evs, 0)
			for {
				time.Sleep(time.Second)
				batch, err := fetch(cursor)
				if err != nil {
					return err
				}
				if len(batch) == 0 {
					continue
				}
				printBatch(batch, false)
				cursor = maxSeq(batch, cursor)
			}
		},
	}
	cmd.Flags().IntVarP(&n, "lines", "n", 100, "number of events to return (0 = all buffered)")
	cmd.Flags().StringVar(&workspace, "workspace", "", "filter by workspace")
	cmd.Flags().StringVar(&team, "team", "", "filter by team")
	cmd.Flags().StringVar(&role, "role", "", "filter by role")
	cmd.Flags().StringVar(&session, "session", "", "filter by session key (workspace/name)")
	cmd.Flags().StringVar(&kind, "kind", "", "filter by event kind (e.g. session.crashed, health.failed, agent.tool.call)")
	cmd.Flags().BoolVar(&warningsOnly, "warnings", false, "show only warning-severity events")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "poll for new events every second until interrupted")
	cmd.Flags().BoolVar(&listKinds, "list-kinds", false, "print every event kind --kind accepts, then exit")
	return cmd
}

// printKindCatalog renders the event-kind catalog in two groups, because
// the split is the one thing an operator scanning the list needs: the
// control-plane kinds report what marvel did to a session, the agent
// kinds report what the agent inside it did, and only the second family
// depends on a runtime marvel can observe.
func printKindCatalog(w io.Writer) {
	var control, agent []events.Kind
	for _, k := range events.AllKinds() {
		if strings.HasPrefix(string(k), "agent.") {
			agent = append(agent, k)
		} else {
			control = append(control, k)
		}
	}
	_, _ = fmt.Fprintf(w, "Control plane (%d): what marvel did to a session\n", len(control))
	for _, k := range control {
		_, _ = fmt.Fprintf(w, "  %s\n", k)
	}
	_, _ = fmt.Fprintf(w, "\nAgent stream (%d): what the agent inside a session did\n", len(agent))
	for _, k := range agent {
		_, _ = fmt.Fprintf(w, "  %s\n", k)
	}
	_, _ = fmt.Fprintf(w, "\nAgent kinds appear only for sessions whose runtime marvel can\nobserve; an interactive pane publishes no stream to parse.\n")
}

// openRotatingLog opens the daemon's on-disk log file with the rlog
// rotating writer. The three size/count ceilings are all opt-out (zero
// means "no limit") so existing `marvel daemon --log-file X` invocations
// keep the default raspi-friendly caps: 10 MiB per file, 5 archives.
//
// The caller is responsible for wiring the returned WriteCloser into
// log.SetOutput — this helper only handles directory creation,
// permission enforcement, and Options construction.
func openRotatingLog(path string, maxSizeMiB, maxFiles, maxTotalMiB int) (io.Closer, error) {
	layout, _ := paths.Default()
	if path == layout.DaemonLog() {
		if err := layout.EnsureLogDir(); err != nil {
			return nil, err
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(path), paths.ModeDir); err != nil {
			return nil, fmt.Errorf("create log dir: %w", err)
		}
	}
	w, err := rlog.Open(path, rlog.Options{
		MaxFileBytes:  int64(maxSizeMiB) * 1024 * 1024,
		MaxFiles:      maxFiles,
		MaxTotalBytes: int64(maxTotalMiB) * 1024 * 1024,
		Mode:          paths.ModeAuthorized,
	})
	if err != nil {
		return nil, fmt.Errorf("open rotating log %s: %w", path, err)
	}
	return w, nil
}

// reapCmd is the deliberate counterpart to the default leave-alone
// posture. It prints what it would destroy and stops; --confirm is what
// actually destroys it.
//
// Showing first is not politeness, it is the ruling. Leaving unrecorded
// state alone was chosen precisely because destruction should be an act
// an operator takes with their eyes open, so a reap that killed on sight
// would put the original failure back behind a new name.
func reapCmd() *cobra.Command {
	var confirm bool
	cmd := &cobra.Command{
		Use:   "reap",
		Short: "List (and with --confirm, destroy) marvel tmux state the daemon does not own",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			params, _ := json.Marshal(map[string]bool{"confirm": confirm})
			resp, err := send(daemon.Request{Method: "reap", Params: params})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}

			var result struct {
				Reaped     bool                  `json:"reaped"`
				Killed     int                   `json:"killed"`
				Candidates []string              `json:"candidates"`
				Orphans    []daemon.OrphanRecord `json:"orphans"`
			}
			if err := json.Unmarshal(resp.Result, &result); err != nil {
				return fmt.Errorf("parse reap result: %w", err)
			}

			if len(result.Candidates) == 0 && len(result.Orphans) == 0 {
				fmt.Println("Nothing to reap: every marvel tmux session is in the daemon's records.")
				return nil
			}

			for _, c := range result.Candidates {
				fmt.Println("  " + c)
			}
			if result.Reaped {
				fmt.Printf("\nReaped %d.\n", result.Killed)
			} else if len(result.Candidates) > 0 {
				fmt.Printf("\n%d unrecorded item(s), left running. Re-run with --confirm to destroy them.\n",
					len(result.Candidates))
				fmt.Println("These may belong to another running daemon. Check before confirming.")
			}

			// Orphans are reported, never reaped: a stale-token presenter is
			// a process the operator owns (aae-orc-m4of). Naming it here so
			// reap accounts for processes as well as panes.
			if len(result.Orphans) > 0 {
				fmt.Printf("\n%d orphaned agent(s) heartbeating against keys this daemon owns "+
					"(reported, not reaped) — see 'marvel orphans'.\n", len(result.Orphans))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&confirm, "confirm", false,
		"actually destroy the listed state (without this, reap only lists)")
	return cmd
}

// orphansCmd reports the orphaned agents heartbeating against session keys
// the daemon owns — a live process presenting a token minted for an
// earlier incarnation of the key. It is the positive, self-announcing
// counterpart to `reap`'s pane scan: the orphan names itself on the
// daemon's own socket (aae-orc-m4of, k58k).
//
// Read-only by design. Marvel reports orphans and never kills them
// (operator ruling 2026-08-09); an orphan is a process the operator owns,
// and the never-destroy-what-marvel-did-not-create rule stands. Stop one
// yourself, or clear its pane with `marvel reap --confirm` once no live
// session depends on it.
func orphansCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "orphans",
		Short: "List agents heartbeating against session keys this daemon owns with a stale token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := send(daemon.Request{Method: "orphans"})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}

			var result struct {
				Orphans []daemon.OrphanRecord `json:"orphans"`
			}
			if err := json.Unmarshal(resp.Result, &result); err != nil {
				return fmt.Errorf("parse orphans result: %w", err)
			}

			if len(result.Orphans) == 0 {
				fmt.Println("No orphaned agents: every heartbeat is from a session this daemon minted.")
				return nil
			}

			now := time.Now()
			w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "SESSION\tWORKSPACE\tTEAM\tROLE\tFIRST SEEN\tLAST SEEN\tREFUSALS")
			for _, o := range result.Orphans {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%d\n",
					o.SessionKey,
					orDash(o.Workspace), orDash(o.Team), orDash(o.Role),
					relTime(now, o.FirstSeen), relTime(now, o.LastSeen), o.Count)
			}
			if err := w.Flush(); err != nil {
				return err
			}
			fmt.Printf("\n%d orphaned agent(s), reported not reaped. These are processes you own; "+
				"stop them yourself, or clear a freed pane with 'marvel reap --confirm'.\n",
				len(result.Orphans))
			return nil
		},
	}
}

// orDash renders an empty coordinate as a dash so a column never collapses.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// relTime renders t as a short "Ns ago" relative to now, the form an
// operator reads for freshness. A last-seen a few seconds old means the
// orphan is still beating; minutes old means it has likely stopped.
func relTime(now, t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

func workCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "work <manifest.toml>",
		Short: "Load a manifest and reconcile desired state",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("read manifest: %w", err)
			}

			params, _ := json.Marshal(map[string]any{"manifest_data": data})
			resp, err := send(daemon.Request{
				Method: "apply",
				Params: params,
			})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}

			var result map[string]string
			_ = json.Unmarshal(resp.Result, &result)
			fmt.Printf("workspace/%s ready\n", result["workspace"])
			return nil
		},
	}
}

func getCmd() *cobra.Command {
	var watchSec string
	cmd := &cobra.Command{
		Use:   "get <resource-type>",
		Short: "List resources (sessions, teams, workspaces, endpoints, policies, budgets)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("watch") {
				secs := 2
				if watchSec != "" {
					if _, err := fmt.Sscanf(watchSec, "%d", &secs); err != nil || secs < 1 {
						return fmt.Errorf("invalid watch interval: %s", watchSec)
					}
				}
				return watchSessionsLoop(time.Duration(secs) * time.Second)
			}
			return getResources(args[0])
		},
	}
	f := cmd.Flags().VarPF(newOptionalString(&watchSec), "watch", "w", "watch sessions (optional: seconds, default 2)")
	f.NoOptDefVal = ""
	return cmd
}

// optionalString implements pflag.Value for a flag with an optional value.
type optionalString struct {
	val *string
}

func newOptionalString(p *string) *optionalString { return &optionalString{val: p} }
func (o *optionalString) String() string          { return *o.val }
func (o *optionalString) Set(s string) error      { *o.val = s; return nil }
func (o *optionalString) Type() string            { return "seconds" }

func getResources(resourceType string) error {
	params, _ := json.Marshal(map[string]string{"resource_type": resourceType})
	resp, err := send(daemon.Request{
		Method: "get",
		Params: params,
	})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}

	switch resourceType {
	case "sessions", "session":
		return printSessions(resp.Result)
	case "teams", "team":
		return printTeams(resp.Result)
	case "workspaces", "workspace":
		return printWorkspaces(resp.Result)
	case "endpoints", "endpoint":
		return printEndpoints(resp.Result)
	case "policies", "policy":
		return printPolicies(resp.Result)
	case "budgets", "budget":
		return printBudgets(resp.Result)
	default:
		fmt.Println(string(resp.Result))
	}
	return nil
}

// planCmd is the read-only preview surface (aae-orc-nrk1): it prints the
// convergence delta the next reconcile tick would enact without spawning or
// deleting anything. It is both an operator preview and a spawn-free way to
// exercise the convergence decision in dev/test.
func planCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "plan [workspace/team]",
		Short: "Preview the convergence delta per team without spawning (dry run)",
		Long: `Print the plan the next reconcile tick would enact — desired vs alive
replica counts, sessions that would spawn, sessions that would scale down, and
any hold (crash-loop backoff or admission refusal) — computed without applying
it. No session is spawned or deleted, so this is a spawn-free way to inspect
what the daemon is about to do.

With no argument every team is shown. An optional workspace/team narrows the
preview; a bare name matches that workspace or that team.

Teams mid-shift are omitted: their convergence runs at shift-aware
generations, which this steady-state preview does not model.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			filter := ""
			if len(args) == 1 {
				filter = args[0]
			}
			return planConvergence(filter)
		},
	}
}

func planConvergence(filter string) error {
	resp, err := send(daemon.Request{Method: "plan"})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	var result struct {
		Plans []team.RolePlan `json:"plans"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return fmt.Errorf("parse plan: %w", err)
	}
	plans := result.Plans
	if filter != "" {
		plans = filterPlans(plans, filter)
	}
	fmt.Print(renderPlanTable(plans))
	return nil
}

// filterPlans narrows a preview to a single workspace/team key. A key with a
// slash matches workspace and team exactly (the form Team.Key() prints); a
// bare token matches either the workspace or the team, so `marvel plan api`
// finds team api in any workspace.
func filterPlans(plans []team.RolePlan, filter string) []team.RolePlan {
	ws, tm, hasSlash := strings.Cut(filter, "/")
	var out []team.RolePlan
	for _, p := range plans {
		match := p.Workspace == filter || p.Team == filter
		if hasSlash {
			match = p.Workspace == ws && p.Team == tm
		}
		if match {
			out = append(out, p)
		}
	}
	return out
}

// renderPlanTable is the pure renderer for `marvel plan`, split out for the
// same reason renderSessionTable and renderBudgetTable are: the table's
// content and its empty-case wording are worth asserting without a daemon.
// Rows are sorted workspace, team, role for a deterministic preview, and a
// summary footer counts the verbs.
func renderPlanTable(plans []team.RolePlan) string {
	if len(plans) == 0 {
		return "no teams to plan (none declared, or all mid-shift)\n"
	}
	sorted := make([]team.RolePlan, len(plans))
	copy(sorted, plans)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Workspace != sorted[j].Workspace {
			return sorted[i].Workspace < sorted[j].Workspace
		}
		if sorted[i].Team != sorted[j].Team {
			return sorted[i].Team < sorted[j].Team
		}
		return sorted[i].Role < sorted[j].Role
	})

	var spawn, scaleDown, hold, steady int
	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "WORKSPACE\tTEAM\tROLE\tGEN\tDESIRED\tACTUAL\tACTION\tSPAWN\tSCALE-DOWN\tHOLD\n")
	for _, p := range sorted {
		holdCell := p.HoldDetail
		if holdCell == "" {
			holdCell = "-"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%d\t%s\t%d\t%d\t%s\n",
			p.Workspace, p.Team, p.Role, p.Generation, p.Desired, p.Actual,
			p.Action, p.Spawn, len(p.Delete), holdCell)
		switch p.Action {
		case team.RoleSpawn:
			spawn++
		case team.RoleScaleDown:
			scaleDown++
		case team.RoleHold:
			hold++
		default:
			steady++
		}
	}
	_ = w.Flush()
	_, _ = fmt.Fprintf(&buf, "\n%d role(s): %d spawn, %d scale-down, %d hold, %d steady\n",
		len(sorted), spawn, scaleDown, hold, steady)
	return buf.String()
}

func describeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "describe <resource-type> <name>",
		Short: "Show detailed information about a resource",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			params, _ := json.Marshal(map[string]string{
				"resource_type": args[0],
				"name":          args[1],
			})
			resp, err := send(daemon.Request{
				Method: "describe",
				Params: params,
			})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}

			// Pretty print JSON.
			var v any
			_ = json.Unmarshal(resp.Result, &v)
			out, _ := json.MarshalIndent(v, "", "  ")
			fmt.Println(string(out))
			return nil
		},
	}
}

func deleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <resource-type> <name>",
		Short: "Delete a resource",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			params, _ := json.Marshal(map[string]string{
				"resource_type": args[0],
				"name":          args[1],
			})
			resp, err := send(daemon.Request{
				Method: "delete",
				Params: params,
			})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			fmt.Printf("%s/%s deleted\n", args[0], args[1])
			return nil
		},
	}
}

func scaleCmd() *cobra.Command {
	var replicas int
	var role string
	cmd := &cobra.Command{
		Use:   "scale <workspace/team>",
		Short: "Scale a team role to N replicas",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			params, _ := json.Marshal(map[string]any{
				"team_key": args[0],
				"role":     role,
				"replicas": replicas,
			})
			resp, err := send(daemon.Request{
				Method: "scale",
				Params: params,
			})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			fmt.Printf("team/%s role/%s scaled to %d replicas\n", args[0], role, replicas)
			return nil
		},
	}
	cmd.Flags().IntVar(&replicas, "replicas", 1, "desired replica count")
	cmd.Flags().StringVar(&role, "role", "", "role to scale (required)")
	return cmd
}

func convergeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "converge [workspace/team]",
		Short: "Release a team (or all teams) from the start-line hold and converge to desired",
		Long: "By default a daemon holds each team at the start line: it adopts surviving\n" +
			"panes but does not spawn toward a team's desired replica count until told to.\n" +
			"converge is that go-line. With no argument it converges every team; with a\n" +
			"workspace/team it converges just that one.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var teamKey string
			if len(args) == 1 {
				teamKey = args[0]
			}
			params, _ := json.Marshal(map[string]any{"team_key": teamKey})
			resp, err := send(daemon.Request{Method: "converge", Params: params})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			var result struct {
				Posture string `json:"posture"`
				Teams   []string
				Roles   []struct {
					Team    string
					Role    string
					Action  string
					Spawn   int
					Desired int
					Actual  int
				}
			}
			_ = json.Unmarshal(resp.Result, &result)
			if len(result.Teams) == 0 {
				fmt.Println("no teams to converge")
				return nil
			}
			fmt.Printf("posture set to %q for %d team(s)\n", result.Posture, len(result.Teams))
			for _, r := range result.Roles {
				if r.Spawn > 0 {
					fmt.Printf("  %s role/%s: spawning %d (%d/%d)\n", r.Team, r.Role, r.Spawn, r.Actual, r.Desired)
				} else {
					fmt.Printf("  %s role/%s: %s (%d/%d)\n", r.Team, r.Role, r.Action, r.Actual, r.Desired)
				}
			}
			return nil
		},
	}
	return cmd
}

func runCmd() *cobra.Command {
	var workspace, team, role, script string
	cmd := &cobra.Command{
		Use:   "run <command> [args...]",
		Short: "Run a one-off agent session",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			params, _ := json.Marshal(map[string]any{
				"workspace":       workspace,
				"team":            team,
				"role":            role,
				"runtime_command": args[0],
				"runtime_args":    args[1:],
				"script":          script,
			})
			resp, err := send(daemon.Request{
				Method: "run",
				Params: params,
			})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			var result map[string]string
			_ = json.Unmarshal(resp.Result, &result)
			fmt.Printf("session/%s created\n", result["session_key"])
			return nil
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", "default", "workspace name")
	cmd.Flags().StringVar(&team, "team", "adhoc", "team name")
	cmd.Flags().StringVar(&role, "role", "adhoc", "role name")
	cmd.Flags().StringVar(&script, "script", "", "Lua script path")
	return cmd
}

func killCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "kill <session-key>",
		Short: "Kill a session (alias for delete session)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			params, _ := json.Marshal(map[string]string{
				"resource_type": "session",
				"name":          args[0],
			})
			resp, err := send(daemon.Request{
				Method: "delete",
				Params: params,
			})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			fmt.Printf("session/%s killed\n", args[0])
			return nil
		},
	}
}

func shiftCmd() *cobra.Command {
	var role string
	cmd := &cobra.Command{
		Use:   "shift <workspace/team>",
		Short: "Initiate a rolling shift for a team",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			params, _ := json.Marshal(map[string]any{
				"team_key": args[0],
				"role":     role,
			})
			resp, err := send(daemon.Request{
				Method: "shift",
				Params: params,
			})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			if role != "" {
				fmt.Printf("shift initiated for team/%s role/%s\n", args[0], role)
			} else {
				fmt.Printf("shift initiated for team/%s (all roles)\n", args[0])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "shift only this role (default: all roles)")
	return cmd
}

func resetHealthCmd() *cobra.Command {
	var role string
	cmd := &cobra.Command{
		Use:   "reset-health <workspace/team>",
		Short: "Clear a role's crash-loop restart count and backoff without deleting the team",
		Long: `Clear a role's accumulated crash-loop state.

A role's RestartCount only ever climbs and drives the backoff window for its
next crash, so a long-lived healthy role that crashed a few times long ago
still gets a lifetime-sized backoff the next time it fails. Success-based decay
resets the count automatically after a role has run healthy for a while; this
verb is the operator override for when you know a role is fine now.

It is also the only way to thaw a role frozen by max_restarts saturation or
restart_policy=never, short of deleting and re-applying the whole team.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			params, _ := json.Marshal(map[string]any{
				"team_key": args[0],
				"role":     role,
			})
			resp, err := send(daemon.Request{
				Method: "reset-health",
				Params: params,
			})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			var res struct {
				Cleared bool `json:"cleared"`
			}
			_ = json.Unmarshal(resp.Result, &res)
			if res.Cleared {
				fmt.Printf("reset health for team/%s role/%s\n", args[0], role)
			} else {
				fmt.Printf("no crash-loop state for team/%s role/%s (nothing to reset)\n", args[0], role)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "role to reset (required)")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

func injectCmd() *cobra.Command {
	var literal, enter bool
	cmd := &cobra.Command{
		Use:   "inject <session-key> <text>",
		Short: "Send keystrokes to a session's pane (executive privilege)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			params, _ := json.Marshal(map[string]any{
				"session_key": args[0],
				"text":        args[1],
				"literal":     literal,
				"enter":       enter,
			})
			resp, err := send(daemon.Request{
				Method: "inject",
				Params: params,
			})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			fmt.Printf("injected %d bytes into %s\n", len(args[1]), args[0])
			return nil
		},
	}
	cmd.Flags().BoolVarP(&literal, "literal", "l", true, "send keys literally (no special key interpretation)")
	cmd.Flags().BoolVarP(&enter, "enter", "e", false, "append Enter keystroke after text")
	return cmd
}

func captureCmd() *cobra.Command {
	var start, end int
	cmd := &cobra.Command{
		Use:   "capture <session-key>",
		Short: "Capture a session's pane content",
		Long: `Capture a session's pane content.

Without flags, captures the visible area. -S with a negative value reaches
into scrollback; an omitted -E defaults to the bottom of the visible area.

Full-screen TUI harnesses (interactive claude and friends) run on the tmux
alternate screen, which has no scrollback — captures of those sessions cap
at the visible screen regardless of -S.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := map[string]any{"session_key": args[0]}
			// Send only the bounds actually given: a defaulted end of 0
			// would pin the span to the TOP visible line (marvel#114).
			if cmd.Flags().Changed("start") {
				p["start"] = start
			}
			if cmd.Flags().Changed("end") {
				p["end"] = end
			}
			params, _ := json.Marshal(p)
			resp, err := send(daemon.Request{
				Method: "capture",
				Params: params,
			})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}

			var result map[string]string
			if err := json.Unmarshal(resp.Result, &result); err != nil {
				return fmt.Errorf("parse result: %w", err)
			}
			fmt.Print(result["content"])
			return nil
		},
	}
	cmd.Flags().IntVarP(&start, "start", "S", 0, "start line (negative for scrollback; default top of visible)")
	cmd.Flags().IntVarP(&end, "end", "E", 0, "end line (default bottom of visible)")
	return cmd
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print marvel version and channel",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("marvel %s (%s)\n", version, channel)
		},
	}
}

func upgradeCmd() *cobra.Command {
	var targetVersion string
	var reexecDaemon bool
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade marvel to the latest version",
		Long: `Upgrade marvel to the latest version.

If installed via Homebrew, delegates to brew upgrade.
Otherwise downloads the latest release from GitHub.

This replaces the binary on disk. A running daemon keeps executing the
old image until it restarts. Pass --daemon to tell the running daemon to
re-exec in place after the install, adopting its live panes so agents
keep running (see 'marvel daemon reexec').`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := upgrade.Run(channel, targetVersion); err != nil {
				return err
			}
			if !reexecDaemon {
				return nil
			}
			resp, err := send(daemon.Request{Method: "reexec"})
			if err != nil {
				return fmt.Errorf("binary upgraded, but sending daemon re-exec failed: %w", err)
			}
			if resp.Error != "" {
				return fmt.Errorf("binary upgraded, but daemon re-exec failed: %s", resp.Error)
			}
			fmt.Println("running daemon re-executing in place; agents keep running")
			return nil
		},
	}
	cmd.Flags().StringVar(&targetVersion, "version", "", "target version (default: latest)")
	cmd.Flags().BoolVar(&reexecDaemon, "daemon", false,
		"after installing, tell the running daemon to re-exec in place so it adopts the new binary without stopping agents")
	return cmd
}

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage marvel cluster configuration",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "tmux-server",
		Short: "Print the tmux server name this HOME's daemon uses",
		Long: `Print the tmux server name (` + "`tmux -L <name>`" + `) for this HOME.

Since marvel #128 each HOME gets its own tmux server, so a bare
` + "`tmux kill-session -t marvel-<workspace>`" + ` reaches the wrong server. Scripts
and runbooks use this to get the name instead of recomputing it:

  tmux -L "$(marvel config tmux-server)" kill-session -t marvel-demo

MARVEL_TMUX_SOCKET overrides the derived name and is reported as-is.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := tmux.SocketName()
			if err != nil {
				return err
			}
			fmt.Println(name)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List configured clusters",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "   \tNAME\tADDRESS\tIDENTITY")
			for _, cl := range cfg.Clusters {
				marker := "  "
				if cl.Name == cfg.CurrentCluster {
					marker = "* "
				}
				// A cluster with neither field set resolves to the
				// layout default at use time. Show what it resolves to,
				// marked, rather than an empty cell: the address column
				// is where an operator checks which daemon they are
				// pointed at, and blank reads as broken.
				addr := cl.Socket
				if cl.Server != "" {
					addr = cl.Server
				}
				if addr == "" {
					addr = config.ResolveSocket() + "  (default)"
				}
				identity := cl.Identity
				if identity == "" {
					identity = "-"
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", marker, cl.Name, addr, identity)
			}
			return w.Flush()
		},
	})

	var addIdentity string
	var addIdentityDefault bool
	addCluster := &cobra.Command{
		Use:   "add-cluster <name> <address>",
		Short: "Add or update a cluster",
		Long: `Add a named cluster to ~/.marvel/config.yaml.

Examples:
  marvel config add-cluster kinu mrvl://kinu
  marvel config add-cluster staging mrvl://deploy@staging.example.com:7000 --identity ~/.marvel/keys/staging_ed25519
  marvel config add-cluster dev /tmp/marvel-dev.sock

For remote (mrvl:// or ssh://) clusters without an --identity flag,
marvel defaults to ~/.marvel/keys/client_ed25519 when that key exists.
Use --no-default-identity to opt out and fall back to SSH_AUTH_SOCK
or ~/.ssh/ keys.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			identity := addIdentity
			if identity == "" && !addIdentityDefault && (strings.HasPrefix(args[1], "mrvl://") || strings.HasPrefix(args[1], "ssh://")) {
				layout, err := paths.Default()
				if err == nil {
					if _, statErr := os.Stat(layout.DefaultClientKey()); statErr == nil {
						identity = layout.DefaultClientKey()
					}
				}
			}
			cfg.AddCluster(args[0], args[1], identity)
			if err := config.Save(cfg); err != nil {
				return err
			}
			if identity != "" {
				fmt.Printf("Cluster %q configured: %s (identity: %s)\n", args[0], args[1], identity)
			} else {
				fmt.Printf("Cluster %q configured: %s\n", args[0], args[1])
			}
			return nil
		},
	}
	addCluster.Flags().StringVar(&addIdentity, "identity", "", "private key file to use for SSH auth on this cluster")
	addCluster.Flags().BoolVar(&addIdentityDefault, "no-default-identity", false, "do not auto-attach ~/.marvel/keys/client_ed25519")
	cmd.AddCommand(addCluster)

	cmd.AddCommand(&cobra.Command{
		Use:   "remove-cluster <name>",
		Short: "Remove a cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if err := cfg.RemoveCluster(args[0]); err != nil {
				return err
			}
			return config.Save(cfg)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "use-cluster <name>",
		Short: "Set the current cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			// Verify it exists.
			if _, err := cfg.ResolveCluster(args[0]); err != nil {
				return err
			}
			cfg.CurrentCluster = args[0]
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Printf("Switched to cluster %q\n", args[0])
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "current",
		Short: "Show the current cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			addr, err := cfg.ResolveCluster("")
			if err != nil {
				return err
			}
			fmt.Printf("%s (%s)\n", cfg.CurrentCluster, addr)
			return nil
		},
	})

	return cmd
}

func keysCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keys",
		Short: "Manage SSH keys for marvel clients and the daemon",
		Long: `Manage marvel SSH key material.

Client-side (on your machine, for connecting to a daemon):
  marvel keys generate          # create ~/.marvel/keys/client_ed25519
  marvel keys show              # print your public key (to send to the admin)
  marvel keys list              # list keys under ~/.marvel/keys/
  marvel keys doctor            # audit and fix ~/.marvel/ permissions

Daemon-side (on the machine running marvel daemon):
  marvel keys authorize <file>  # add a client's pubkey to authorized_keys
  marvel keys authorized        # list authorized clients
  marvel keys revoke <fp>       # remove a client by fingerprint
  marvel keys host-fingerprint  # print this daemon's host key fingerprint`,
	}

	// Client-side: keys generate
	var genName, genType, genComment string
	var genForce bool
	generate := &cobra.Command{
		Use:   "generate",
		Short: "Generate a new client keypair under ~/.marvel/keys/",
		RunE: func(cmd *cobra.Command, args []string) error {
			layout, err := paths.Default()
			if err != nil {
				return err
			}
			ck, err := keys.GenerateClient(layout, keys.GenerateOptions{
				Name:    genName,
				Type:    keys.KeyType(genType),
				Comment: genComment,
				Force:   genForce,
			})
			if err != nil {
				return err
			}
			fmt.Printf("Generated %s keypair\n", ck.Type)
			fmt.Printf("  private: %s\n", ck.PrivatePath)
			fmt.Printf("  public:  %s\n", ck.PublicPath)
			fmt.Printf("  fingerprint: %s\n", ck.Fingerprint)
			fmt.Printf("  comment: %s\n", ck.Comment)
			fmt.Println()
			fmt.Println("Share your public key with the daemon admin:")
			fmt.Printf("  marvel keys show%s | pbcopy\n", nameArg(ck.Name))
			fmt.Println("Then on the daemon machine:")
			fmt.Printf("  marvel keys authorize <your-pubkey.pub>\n")
			return nil
		},
	}
	generate.Flags().StringVar(&genName, "name", paths.DefaultClientKeyName, "key name (filename under ~/.marvel/keys/)")
	generate.Flags().StringVar(&genType, "type", string(keys.KeyTypeEd25519), "key type (ed25519)")
	generate.Flags().StringVar(&genComment, "comment", "", "embedded comment (default: user@host)")
	generate.Flags().BoolVar(&genForce, "force", false, "overwrite an existing key")
	cmd.AddCommand(generate)

	// Client-side: keys show
	var showName string
	show := &cobra.Command{
		Use:   "show",
		Short: "Print a client public key (to share with the daemon admin)",
		RunE: func(cmd *cobra.Command, args []string) error {
			layout, err := paths.Default()
			if err != nil {
				return err
			}
			ck, err := keys.LoadClient(layout, showName)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(ck.PublicPath)
			if err != nil {
				return err
			}
			fmt.Print(string(data))
			return nil
		},
	}
	show.Flags().StringVar(&showName, "name", paths.DefaultClientKeyName, "key name")
	cmd.AddCommand(show)

	// Client-side: keys list
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List client keypairs under ~/.marvel/keys/",
		RunE: func(cmd *cobra.Command, args []string) error {
			layout, err := paths.Default()
			if err != nil {
				return err
			}
			clients, err := keys.ListClient(layout)
			if err != nil {
				return err
			}
			if len(clients) == 0 {
				fmt.Println("No client keys. Create one with: marvel keys generate")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "NAME\tTYPE\tFINGERPRINT\tCOMMENT")
			for _, k := range clients {
				comment := k.Comment
				if comment == "" {
					comment = "(no comment)"
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", k.Name, k.Type, k.Fingerprint, comment)
			}
			return w.Flush()
		},
	})

	// Client-side: keys doctor
	var fix bool
	doctor := &cobra.Command{
		Use:   "doctor",
		Short: "Audit (and optionally fix) permissions under ~/.marvel/",
		RunE: func(cmd *cobra.Command, args []string) error {
			layout, err := paths.Default()
			if err != nil {
				return err
			}
			issues, err := layout.Audit()
			if err != nil {
				return err
			}
			if len(issues) == 0 {
				fmt.Printf("OK — %s\n", layout.Home)
				return nil
			}
			fmt.Printf("Found %d permission issue(s) in %s:\n", len(issues), layout.Home)
			for _, i := range issues {
				fmt.Printf("  %s: mode %o, want %o (%s)\n", i.Path, i.Got, i.Want, i.Reason)
			}
			if !fix {
				fmt.Println("\nRun 'marvel keys doctor --fix' to repair.")
				return fmt.Errorf("permission issues found")
			}
			remaining := layout.Repair(issues)
			if len(remaining) > 0 {
				for _, i := range remaining {
					fmt.Printf("  FAILED: %s\n", i.Error())
				}
				return fmt.Errorf("%d issue(s) could not be repaired", len(remaining))
			}
			fmt.Println("Repaired.")
			return nil
		},
	}
	doctor.Flags().BoolVar(&fix, "fix", false, "repair permissions to their expected modes")
	cmd.AddCommand(doctor)

	// Daemon-side: keys authorize (formerly: add)
	authorize := &cobra.Command{
		Use:     "authorize <public-key-file>",
		Aliases: []string{"add"},
		Short:   "Authorize a client's public key on this daemon",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("read key file: %w", err)
			}
			comment := args[0]
			return daemon.AddAuthorizedKey(data, comment)
		},
	}
	cmd.AddCommand(authorize)

	// Daemon-side: keys authorized (formerly: list)
	cmd.AddCommand(&cobra.Command{
		Use:     "authorized",
		Aliases: []string{"list-authorized"},
		Short:   "List clients authorized on this daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			authed, err := daemon.ListAuthorizedKeys()
			if err != nil {
				return err
			}
			if len(authed) == 0 {
				fmt.Println("No authorized keys. Add one with: marvel keys authorize <pubkey-file>")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "FINGERPRINT\tTYPE\tCOMMENT")
			for _, k := range authed {
				comment := k.Comment
				if comment == "" {
					comment = "(no comment)"
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", k.Fingerprint, k.Type, comment)
			}
			return w.Flush()
		},
	})

	// Daemon-side: keys revoke (formerly: remove)
	cmd.AddCommand(&cobra.Command{
		Use:     "revoke <fingerprint>",
		Aliases: []string{"remove"},
		Short:   "Revoke a client by fingerprint",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return daemon.RemoveAuthorizedKey(args[0])
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "host-fingerprint",
		Short: "Print this daemon's SSH host key fingerprint",
		RunE: func(cmd *cobra.Command, args []string) error {
			fp, err := daemon.HostKeyFingerprint()
			if err != nil {
				return err
			}
			fmt.Println(fp)
			return nil
		},
	})

	// keys trust — add a cluster's host key to ~/.marvel/known_hosts
	// without prompting. Intended for non-interactive bootstraps where
	// the admin has already confirmed the fingerprint out-of-band.
	trust := &cobra.Command{
		Use:   "trust [cluster]",
		Short: "Trust and record a cluster's host key in ~/.marvel/known_hosts",
		Long: `Connect to the named cluster (or the current one) and add its
host key to ~/.marvel/known_hosts without prompting.

Use 'marvel keys host-fingerprint' on the daemon machine and compare
the fingerprint that 'marvel keys trust' prints before relying on
the connection.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := clusterName
			if len(args) == 1 {
				name = args[0]
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			cl, err := cfg.GetCluster(name)
			if err != nil {
				return err
			}
			if cl == nil || cl.Server == "" {
				return fmt.Errorf("cluster %q has no mrvl:// address; nothing to trust", name)
			}
			addr := cl.Server
			opts := daemon.DialOptions{Identity: cl.Identity, TrustUnknownHost: true}
			if identityPath != "" {
				opts.Identity = identityPath
			}
			resp, err := daemon.SendRequestWith(addr, daemon.Request{
				Method: "get",
				Params: json.RawMessage(`{"resource_type":"workspaces"}`),
			}, opts)
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			fmt.Printf("Host key for %s trusted and recorded.\n", addr)
			return nil
		},
	}
	cmd.AddCommand(trust)

	return cmd
}

// nameArg returns " --name <name>" for the default help string when the
// key is non-default, and "" otherwise.
func nameArg(name string) string {
	if name == "" || name == paths.DefaultClientKeyName {
		return ""
	}
	return " --name " + name
}

func stopCmd() *cobra.Command {
	var teardown bool
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the marvel daemon, leaving agents running",
		Long: `Stop the marvel daemon.

By default this detaches: the daemon checkpoints its state and exits
while every agent keeps running in its tmux pane. The next
'marvel daemon' start reads that state back and adopts the live panes,
so a restart or an upgrade costs no agent context.

Use --teardown when you want the machine clean. Every session this
daemon recorded is deleted and every recorded workspace's tmux session
killed before it exits, so it leaves nothing of its own to adopt.
marvel-* tmux state it never recorded is reported rather than
destroyed; 'marvel daemon --reclaim' and 'marvel reap --confirm' are
the acts that destroy that.`,
		Example: `  marvel stop              # detach, agents keep running
  marvel stop --teardown   # end every agent, then stop`,
		RunE: func(cmd *cobra.Command, args []string) error {
			params, _ := json.Marshal(map[string]bool{"teardown": teardown})
			resp, err := send(daemon.Request{Method: "stop", Params: params})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			if !teardown {
				fmt.Println("marvel daemon detaching, agents keep running")
				return nil
			}
			fmt.Println("marvel daemon stopping, agents torn down")
			// A daemon old enough to send no body has nothing to report,
			// and the teardown it just ran still happened: do not turn
			// that into a failed command.
			if len(resp.Result) == 0 {
				return nil
			}
			var result daemon.StopResult
			if uerr := json.Unmarshal(resp.Result, &result); uerr != nil {
				return fmt.Errorf("decode stop result: %w", uerr)
			}
			if w := stopWarning(result.Unowned); w != "" {
				fmt.Fprintln(os.Stderr, w)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&teardown, "teardown", false,
		"delete every session and kill its tmux session before stopping")
	return cmd
}

// stopWarning renders what a teardown leaves standing. Returns an empty
// string when there is nothing to report, so the common case stays quiet.
//
// Teardown removes what the daemon recorded; marvel-* tmux state it never
// recorded survives by design (docs/design/daemon-isolation.md Decision
// 5). Saying so is the difference between "agents torn down" being a
// report and being a guess. See ArcavenAE/marvel#92.
func stopWarning(unowned []string) string {
	if len(unowned) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "warning: teardown removes only what this daemon recorded; %d marvel tmux item(s) survive it:\n",
		len(unowned))
	for _, item := range unowned {
		fmt.Fprintf(&b, "  %s\n", item)
	}
	fmt.Fprint(&b, "clear them with 'marvel daemon --reclaim' or 'marvel reap --confirm'")
	return b.String()
}

// --- Watch mode ---

type watchSort struct {
	column       string
	desc         bool
	showHelp     bool
	lastSessions []api.Session
}

func toggleSort(ws *watchSort, col string, descFirst bool) {
	if ws.column == col {
		ws.desc = !ws.desc
	} else {
		ws.column = col
		ws.desc = descFirst
	}
}

func sortSessions(sessions []api.Session, ws *watchSort) {
	sort.Slice(sessions, func(i, j int) bool {
		var less bool
		switch ws.column {
		case "context":
			less = sessions[i].ContextPercent < sessions[j].ContextPercent
		case "cpu":
			less = sessions[i].CPUPercent < sessions[j].CPUPercent
		case "rss":
			less = sessions[i].RSSBytes < sessions[j].RSSBytes
		case "name":
			less = sessions[i].Name < sessions[j].Name
		case "team":
			less = sessions[i].Team < sessions[j].Team
		case "role":
			less = sessions[i].Role < sessions[j].Role
		case "generation":
			less = sessions[i].Generation < sessions[j].Generation
		case "workspace":
			less = sessions[i].Workspace < sessions[j].Workspace
		case "state":
			less = string(sessions[i].State) < string(sessions[j].State)
		case "runtime":
			ai, aj := sessions[i].Runtime.Name, sessions[j].Runtime.Name
			if ai == "" {
				ai = sessions[i].Runtime.Command
			}
			if aj == "" {
				aj = sessions[j].Runtime.Command
			}
			less = ai < aj
		case "llm":
			less = sessions[i].ContextModel < sessions[j].ContextModel
		case "health":
			hi, hj := string(sessions[i].HealthState), string(sessions[j].HealthState)
			if hi == "" {
				hi = "unknown"
			}
			if hj == "" {
				hj = "unknown"
			}
			less = hi < hj
		case "desk":
			less = sessions[i].PaneID < sessions[j].PaneID
		default:
			less = sessions[i].Name < sessions[j].Name
		}
		if ws.desc {
			return !less
		}
		return less
	})
}

func fetchSessions() ([]api.Session, error) {
	params, _ := json.Marshal(map[string]string{"resource_type": "sessions"})
	resp, err := send(daemon.Request{
		Method: "get",
		Params: params,
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	var sessions []api.Session
	if err := json.Unmarshal(resp.Result, &sessions); err != nil {
		return nil, err
	}
	return sessions, nil
}

// formatBytes renders a byte count in the units an operator scanning a
// column wants: three significant figures at most, no padding.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	value := float64(n)
	for _, suffix := range []string{"K", "M", "G", "T"} {
		value /= unit
		if value < unit {
			if value < 10 {
				return fmt.Sprintf("%.1f%s", value, suffix)
			}
			return fmt.Sprintf("%.0f%s", value, suffix)
		}
	}
	return fmt.Sprintf("%.0fP", value/unit)
}

func renderSessionTable(sessions []api.Session) string {
	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "WORKSPACE\tTEAM\tROLE\tGEN\tAGENT NAME\tSTATE\tHEALTH\tCTX%%\tCPU%%\tRSS\tDESK\tRUNTIME\tLLM\n")
	for _, s := range sessions {
		runtimeName := s.Runtime.Name
		if runtimeName == "" {
			runtimeName = s.Runtime.Command
		}
		// LLM is the model as the metering producer named it: the
		// stream accountant's raw model for headless sessions, the
		// statusline feed's display name for interactive ones.
		llm := s.ContextModel
		if llm == "" {
			llm = "-"
		}
		// CTX% has two producers: the cooperative heartbeat RPC (the
		// statusline/codex feeds and the simulator) and the usage
		// accountant fed by adapter streams. Both stamp ContextAt, so that
		// is the single "measured" sentinel.
		//
		// Three states, not two. "-" means marvel never measured this
		// session's context: no stream, so an interactive launch, a pane
		// adopted from a prior daemon, or a non-stream adapter. "?" means
		// the tokens are real but the model's window could not be
		// resolved, so a percentage would be a fiction against a guessed
		// denominator. `marvel describe session` carries the reason, and
		// the fix is usually one runtime.context_window line.
		//
		// Keyed on the DECLARED producer (ContextSource), not on which
		// fields happen to be populated. The two producers report
		// different shapes, and an accountant reading with an unresolved
		// window is shaped like a heartbeat, so inferring from shape
		// rendered real cooperative readings as "?". See aae-orc-ibu9.
		ctx := "-"
		switch {
		case s.ContextAt.IsZero():
		case s.ContextLimit == 0 && s.ContextSource != api.ContextSourceHeartbeat:
			// No window resolved, so a percentage would be a fiction.
			// The heartbeat is the one legitimate exception: a feed that
			// carries a window now resolves one (aae-orc-38yr) and lands in
			// the default branch like any measured reading, but a
			// percentage-only heartbeat (the simulator, a feed too young to
			// have classes) reports a figure the agent computed itself and
			// never needed a window to do. Stating the exception explicitly
			// is what stops that reading being rendered absent.
			ctx = "?"
		default:
			ctx = fmt.Sprintf("%.0f%%", s.ContextPercent)
		}
		// Likewise for the sampler: a session the sampler has not
		// reached (no pid, or a platform with no process table reader)
		// shows absence rather than an idle-looking zero.
		cpu, rss := "-", "-"
		if !s.MetricsAt.IsZero() {
			cpu = fmt.Sprintf("%.1f", s.CPUPercent)
			rss = formatBytes(s.RSSBytes)
		}
		desk := strings.TrimPrefix(s.PaneID, "%")
		gen := fmt.Sprintf("%d", s.Generation)
		health := string(s.HealthState)
		if health == "" {
			health = "unknown"
		}
		// Activity is an orthogonal, restart-neutral advisory (aae-orc-9box).
		// HEALTH is LIVENESS — the process is alive and its pane exists — so a
		// stalled session still reads healthy/unknown here; the "(stalled)"
		// suffix says marvel has not observed it do work within its role's
		// activity_timeout, which liveness cannot tell you. Running sessions
		// only: a terminated session's last advisory is not news.
		if s.State == api.SessionRunning && s.ActivityState == api.ActivityStalled {
			health += " (stalled)"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			s.Workspace, s.Team, s.Role, gen, s.Name, s.State, health, ctx, cpu, rss, desk, runtimeName, llm)
	}
	_ = w.Flush()
	return buf.String()
}

func renderWatch(ws *watchSort, interval time.Duration) string {
	var buf bytes.Buffer

	fmt.Fprintf(&buf, "marvel get sessions    %s    (every %v)\n",
		time.Now().Format("15:04:05"), interval)

	if ws.showHelp {
		fmt.Fprintf(&buf, "\n")
		fmt.Fprintf(&buf, "  Sort keys (toggle asc/desc):\n")
		fmt.Fprintf(&buf, "    w  workspace      t  team          R  role\n")
		fmt.Fprintf(&buf, "    g  generation     n  agent name    s  state\n")
		fmt.Fprintf(&buf, "    c  context        d  desk          r  runtime\n")
		fmt.Fprintf(&buf, "    l  llm            h  health\n")
		fmt.Fprintf(&buf, "    p  cpu            m  memory (rss)\n")
		fmt.Fprintf(&buf, "\n")
		fmt.Fprintf(&buf, "  HEALTH is liveness (process + pane), not productivity. A live\n")
		fmt.Fprintf(&buf, "  process at a login prompt still reads healthy. \"(stalled)\" marks a\n")
		fmt.Fprintf(&buf, "  session marvel has not seen do work within its role's activity_timeout.\n")
		fmt.Fprintf(&buf, "\n")
		fmt.Fprintf(&buf, "    ?  toggle help    q  quit\n")
		fmt.Fprintf(&buf, "\n")
		return buf.String()
	}

	sortLabel := ws.column
	if ws.desc {
		sortLabel += " desc"
	} else {
		sortLabel += " asc"
	}
	fmt.Fprintf(&buf, "sort: %s    ?:help  q:quit\n\n", sortLabel)

	sessions, err := fetchSessions()
	if err != nil {
		fmt.Fprintf(&buf, "⚠ daemon disconnected — waiting for reconnect\n\n")
		if len(ws.lastSessions) > 0 {
			fmt.Fprintf(&buf, "last known state:\n")
			sortSessions(ws.lastSessions, ws)
			buf.WriteString(renderSessionTable(ws.lastSessions))
		}
		return buf.String()
	}

	ws.lastSessions = sessions
	sortSessions(sessions, ws)
	buf.WriteString(renderSessionTable(sessions))
	return buf.String()
}

func watchSessionsLoop(interval time.Duration) error {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return fmt.Errorf("watch mode requires a terminal")
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("enable raw mode: %w", err)
	}
	defer func() { _ = term.Restore(fd, oldState) }()

	// Read keys in a goroutine.
	keys := make(chan byte, 1)
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				keys <- buf[0]
			}
			if err != nil {
				return
			}
		}
	}()

	ws := &watchSort{column: "name", desc: false}

	render := func() {
		output := renderWatch(ws, interval)
		// Raw mode needs \r\n instead of \n.
		output = strings.ReplaceAll(output, "\n", "\r\n")
		// Clear screen, cursor to top.
		fmt.Print("\033[2J\033[H")
		fmt.Print(output)
	}

	render()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case key := <-keys:
			switch key {
			case 'q', 3: // q or Ctrl-C
				fmt.Print("\033[2J\033[H")
				return nil
			case 'c':
				toggleSort(ws, "context", true)
			case 'p':
				toggleSort(ws, "cpu", true)
			case 'm':
				toggleSort(ws, "rss", true)
			case 'n':
				toggleSort(ws, "name", false)
			case 'r':
				toggleSort(ws, "runtime", false)
			case 'R':
				toggleSort(ws, "role", false)
			case 'l':
				toggleSort(ws, "llm", false)
			case 'g':
				toggleSort(ws, "generation", false)
			case 't':
				toggleSort(ws, "team", false)
			case 'w':
				toggleSort(ws, "workspace", false)
			case 's':
				toggleSort(ws, "state", false)
			case 'd':
				toggleSort(ws, "desk", false)
			case 'h':
				toggleSort(ws, "health", false)
			case '?':
				ws.showHelp = !ws.showHelp
			default:
				continue
			}
			render()
		case <-ticker.C:
			if !ws.showHelp {
				render()
			}
		}
	}
}

// --- Table printers (non-watch) ---

func printSessions(data json.RawMessage) error {
	var sessions []api.Session
	if err := json.Unmarshal(data, &sessions); err != nil {
		return err
	}
	// Default sort: name ascending
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Name < sessions[j].Name
	})
	fmt.Print(renderSessionTable(sessions))
	return nil
}

func printTeams(data json.RawMessage) error {
	var teams []api.Team
	if err := json.Unmarshal(data, &teams); err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "WORKSPACE\tTEAM\tROLE\tREPLICAS\tRUNTIME\n")
	for _, t := range teams {
		for _, r := range t.Roles {
			rt := r.Runtime.Name
			if rt == "" {
				rt = r.Runtime.Command
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", t.Workspace, t.Name, r.Name, r.Replicas, rt)
		}
	}
	return w.Flush()
}

func printWorkspaces(data json.RawMessage) error {
	var workspaces []api.Workspace
	if err := json.Unmarshal(data, &workspaces); err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "NAME\tAGE\n")
	for _, ws := range workspaces {
		age := "unknown"
		if !ws.CreatedAt.IsZero() {
			age = strings.TrimSuffix(fmt.Sprintf("%v", ws.CreatedAt.Format("2006-01-02 15:04")), " ")
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\n", ws.Name, age)
	}
	return w.Flush()
}

func printEndpoints(data json.RawMessage) error {
	var endpoints []api.Endpoint
	if err := json.Unmarshal(data, &endpoints); err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "WORKSPACE\tNAME\tTEAM\n")
	for _, e := range endpoints {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", e.Workspace, e.Name, e.Team)
	}
	return w.Flush()
}

func printPolicies(data json.RawMessage) error {
	var policies []api.Policy
	if err := json.Unmarshal(data, &policies); err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "WORKSPACE\tNAME\tVERSION\tKEYS\n")
	for _, p := range policies {
		version := p.Version
		if version == "" {
			version = "-"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\n", p.Workspace, p.Name, version, len(p.Settings))
	}
	return w.Flush()
}

// printBudgets renders `marvel get budgets`: one row per declared budget
// dimension, with what has been observed against it and that dimension's
// state (ok, at-ceiling, refusing, unmetered). The surface that answers
// "which dimension tripped and by how much" — the event says a refusal
// happened, this says where the team stands. at-ceiling and refusing are
// deliberately separate: a team sized at its ceiling refuses nothing.
func printBudgets(data json.RawMessage) error {
	var rows []admission.Row
	if err := json.Unmarshal(data, &rows); err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("no teams declare a budget")
		return nil
	}
	fmt.Print(renderBudgetTable(rows))
	return nil
}

// renderBudgetTable is the pure renderer, split out for the same reason
// renderSessionTable is: the table's absence handling is worth asserting
// without a daemon.
func renderBudgetTable(rows []admission.Row) string {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Workspace != rows[j].Workspace {
			return rows[i].Workspace < rows[j].Workspace
		}
		if rows[i].Team != rows[j].Team {
			return rows[i].Team < rows[j].Team
		}
		return rows[i].Dimension < rows[j].Dimension
	})
	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "WORKSPACE\tTEAM\tDIMENSION\tLIMIT\tOBSERVED\tHEADROOM\tSTATE\tWINDOW\tNOTE\n")
	for _, r := range rows {
		note := r.Note
		if note == "" {
			note = "-"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%d\t%s\t%s\t%s\n",
			r.Workspace, r.Team, r.Dimension, r.Limit, r.Observed, r.Headroom,
			r.State, formatWindow(r.Window), note)
	}
	_ = w.Flush()
	return buf.String()
}

// formatWindow renders how long a cumulative figure has been accumulating.
// A count-shaped dimension has no window and renders a dash, as does a
// dimension nothing has been measured for yet. The accountant is in-memory,
// so this span restarts with the daemon; showing it is what keeps a reset
// visible instead of silent.
func formatWindow(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return time.Since(t).Round(time.Second).String()
}

// stripComments removes shell-style comments from CLI arguments.
// Everything from a bare "#" argument onward is dropped, so that
// inline notes work: ./marvel shift test/squad  # replace all workers
func stripComments(args []string) []string {
	for i, arg := range args {
		if i == 0 {
			continue // skip the binary name
		}
		if arg == "#" || strings.HasPrefix(arg, "#") {
			return args[:i]
		}
	}
	return args
}
