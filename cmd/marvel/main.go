package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/arcaven/marvel/internal/api"
	"github.com/arcaven/marvel/internal/daemon"
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
	root.AddCommand(applyCmd())
	root.AddCommand(getCmd())
	root.AddCommand(describeCmd())
	root.AddCommand(deleteCmd())
	root.AddCommand(scaleCmd())
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

func applyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "apply <manifest.toml>",
		Short: "Apply a manifest to declare desired state",
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
			fmt.Printf("workspace/%s applied\n", result["workspace"])
			return nil
		},
	}
}

func getCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <resource-type>",
		Short: "List resources (sessions, teams, workspaces, endpoints)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			params, _ := json.Marshal(map[string]string{"resource_type": args[0]})
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

			switch args[0] {
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
		},
	}
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

// Table printers

func printSessions(data json.RawMessage) error {
	var sessions []api.Session
	if err := json.Unmarshal(data, &sessions); err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "WORKSPACE\tNAME\tTEAM\tSTATE\tRUNTIME\tPANE\n")
	for _, s := range sessions {
		rt := s.Runtime.Name
		if rt == "" {
			rt = s.Runtime.Command
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			s.Workspace, s.Name, s.Team, s.State, rt, s.PaneID)
	}
	return w.Flush()
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
