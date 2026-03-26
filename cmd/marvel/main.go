package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemon"
	"github.com/spf13/cobra"
)

var socketPath = daemon.DefaultSocket

func main() {
	root := &cobra.Command{
		Use:   "marvel",
		Short: "Agent orchestration control plane",
	}

	root.PersistentFlags().StringVar(&socketPath, "socket", socketPath, "daemon socket path")

	root.AddCommand(daemonCmd())
	root.AddCommand(workCmd())
	root.AddCommand(getCmd())
	root.AddCommand(describeCmd())
	root.AddCommand(deleteCmd())
	root.AddCommand(scaleCmd())
	root.AddCommand(runCmd())
	root.AddCommand(killCmd())
	root.AddCommand(stopCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func daemonCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "daemon",
		Short: "Start the marvel daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := daemon.New()
			if err != nil {
				return err
			}
			if err := d.Start(socketPath); err != nil {
				return err
			}

			// Wait for signal.
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
			<-sig
			fmt.Println("\nshutting down...")
			d.Stop()
			return nil
		},
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
			resp, err := daemon.SendRequest(socketPath, daemon.Request{
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
			json.Unmarshal(resp.Result, &result)
			fmt.Printf("workspace/%s ready\n", result["workspace"])
			return nil
		},
	}
}

func getCmd() *cobra.Command {
	var watchSec string
	cmd := &cobra.Command{
		Use:   "get <resource-type>",
		Short: "List resources (sessions, teams, workspaces, endpoints)",
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
func (o *optionalString) String() string           { return *o.val }
func (o *optionalString) Set(s string) error        { *o.val = s; return nil }
func (o *optionalString) Type() string              { return "seconds" }

func getResources(resourceType string) error {
	params, _ := json.Marshal(map[string]string{"resource_type": resourceType})
	resp, err := daemon.SendRequest(socketPath, daemon.Request{
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
	default:
		fmt.Println(string(resp.Result))
	}
	return nil
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
			resp, err := daemon.SendRequest(socketPath, daemon.Request{
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
			json.Unmarshal(resp.Result, &v)
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
			resp, err := daemon.SendRequest(socketPath, daemon.Request{
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
	cmd := &cobra.Command{
		Use:   "scale <workspace/team>",
		Short: "Scale a team to N replicas",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			params, _ := json.Marshal(map[string]any{
				"team_key": args[0],
				"replicas": replicas,
			})
			resp, err := daemon.SendRequest(socketPath, daemon.Request{
				Method: "scale",
				Params: params,
			})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			fmt.Printf("team/%s scaled to %d replicas\n", args[0], replicas)
			return nil
		},
	}
	cmd.Flags().IntVar(&replicas, "replicas", 1, "desired replica count")
	return cmd
}

func runCmd() *cobra.Command {
	var workspace, team, script string
	cmd := &cobra.Command{
		Use:   "run <command> [args...]",
		Short: "Run a one-off agent session",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			params, _ := json.Marshal(map[string]any{
				"workspace":       workspace,
				"team":            team,
				"runtime_command": args[0],
				"runtime_args":    args[1:],
				"script":          script,
			})
			resp, err := daemon.SendRequest(socketPath, daemon.Request{
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
			json.Unmarshal(resp.Result, &result)
			fmt.Printf("session/%s created\n", result["session_key"])
			return nil
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", "default", "workspace name")
	cmd.Flags().StringVar(&team, "team", "adhoc", "team name")
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
			resp, err := daemon.SendRequest(socketPath, daemon.Request{
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

func stopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the marvel daemon and clean up all resources",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := daemon.SendRequest(socketPath, daemon.Request{Method: "stop"})
			if err != nil {
				return err
			}
			if resp.Error != "" {
				return fmt.Errorf("%s", resp.Error)
			}
			fmt.Println("marvel daemon stopping")
			return nil
		},
	}
}

// --- Watch mode ---

type watchSort struct {
	column   string
	desc     bool
	showHelp bool
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
		case "name":
			less = sessions[i].Name < sessions[j].Name
		case "team":
			less = sessions[i].Team < sessions[j].Team
		case "workspace":
			less = sessions[i].Workspace < sessions[j].Workspace
		case "state":
			less = string(sessions[i].State) < string(sessions[j].State)
		case "agent":
			ai, aj := sessions[i].Runtime.Name, sessions[j].Runtime.Name
			if ai == "" {
				ai = sessions[i].Runtime.Command
			}
			if aj == "" {
				aj = sessions[j].Runtime.Command
			}
			less = ai < aj
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
	resp, err := daemon.SendRequest(socketPath, daemon.Request{
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

func renderSessionTable(sessions []api.Session) string {
	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "WORKSPACE\tTEAM\tNAME\tSTATE\tCONTEXT%%\tDESK\tAGENT\n")
	for _, s := range sessions {
		agent := s.Runtime.Name
		if agent == "" {
			agent = s.Runtime.Command
		}
		ctx := "-"
		if s.ContextPercent > 0 || !s.LastHeartbeat.IsZero() {
			ctx = fmt.Sprintf("%.0f%%", s.ContextPercent)
		}
		desk := s.PaneID
		if strings.HasPrefix(desk, "%") {
			desk = desk[1:]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			s.Workspace, s.Team, s.Name, s.State, ctx, desk, agent)
	}
	w.Flush()
	return buf.String()
}

func sortIndicator(ws *watchSort, col string) string {
	if ws.column == col {
		if ws.desc {
			return " v"
		}
		return " ^"
	}
	return ""
}

func renderWatch(ws *watchSort, interval time.Duration) string {
	var buf bytes.Buffer

	fmt.Fprintf(&buf, "marvel get sessions    %s    (every %v)\n",
		time.Now().Format("15:04:05"), interval)

	if ws.showHelp {
		fmt.Fprintf(&buf, "\n")
		fmt.Fprintf(&buf, "  Sort keys (toggle asc/desc):\n")
		fmt.Fprintf(&buf, "    w  workspace      t  team          n  name\n")
		fmt.Fprintf(&buf, "    s  state          c  context       d  desk\n")
		fmt.Fprintf(&buf, "    a  agent\n")
		fmt.Fprintf(&buf, "\n")
		fmt.Fprintf(&buf, "    h  toggle help    q  quit\n")
		fmt.Fprintf(&buf, "\n")
		return buf.String()
	}

	sortLabel := ws.column
	if ws.desc {
		sortLabel += " desc"
	} else {
		sortLabel += " asc"
	}
	fmt.Fprintf(&buf, "sort: %s    h:help  q:quit\n\n", sortLabel)

	sessions, err := fetchSessions()
	if err != nil {
		fmt.Fprintf(&buf, "error: %v\n", err)
		return buf.String()
	}

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
	defer term.Restore(fd, oldState)

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
			case 'n':
				toggleSort(ws, "name", false)
			case 't':
				toggleSort(ws, "team", false)
			case 'w':
				toggleSort(ws, "workspace", false)
			case 's':
				toggleSort(ws, "state", false)
			case 'a':
				toggleSort(ws, "agent", false)
			case 'd':
				toggleSort(ws, "desk", false)
			case 'h':
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
	fmt.Fprintf(w, "WORKSPACE\tNAME\tREPLICAS\tRUNTIME\n")
	for _, t := range teams {
		rt := t.Runtime.Name
		if rt == "" {
			rt = t.Runtime.Command
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", t.Workspace, t.Name, t.Replicas, rt)
	}
	return w.Flush()
}

func printWorkspaces(data json.RawMessage) error {
	var workspaces []api.Workspace
	if err := json.Unmarshal(data, &workspaces); err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "NAME\tAGE\n")
	for _, ws := range workspaces {
		age := "unknown"
		if !ws.CreatedAt.IsZero() {
			age = strings.TrimSuffix(fmt.Sprintf("%v", ws.CreatedAt.Format("2006-01-02 15:04")), " ")
		}
		fmt.Fprintf(w, "%s\t%s\n", ws.Name, age)
	}
	return w.Flush()
}

func printEndpoints(data json.RawMessage) error {
	var endpoints []api.Endpoint
	if err := json.Unmarshal(data, &endpoints); err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "WORKSPACE\tNAME\tTEAM\n")
	for _, e := range endpoints {
		fmt.Fprintf(w, "%s\t%s\t%s\n", e.Workspace, e.Name, e.Team)
	}
	return w.Flush()
}
