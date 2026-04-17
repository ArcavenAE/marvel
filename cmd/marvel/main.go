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
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemon"
	"github.com/arcavenae/marvel/internal/keys"
	"github.com/arcavenae/marvel/internal/paths"
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
func resolveDaemon() (string, daemon.DialOptions) {
	if socketPath != "" {
		return socketPath, daemon.DialOptions{Identity: identityPath}
	}
	cfg, err := config.Load()
	if err != nil {
		return config.DefaultSocket, daemon.DialOptions{Identity: identityPath}
	}
	cl, err := cfg.GetCluster(clusterName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
		return config.DefaultSocket, daemon.DialOptions{Identity: identityPath}
	}
	if cl == nil {
		return config.DefaultSocket, daemon.DialOptions{Identity: identityPath}
	}
	addr := cl.Socket
	if cl.Server != "" {
		addr = cl.Server
	}
	if addr == "" {
		addr = config.DefaultSocket
	}
	id := identityPath
	if id == "" {
		id = cl.Identity
	}
	return addr, daemon.DialOptions{Identity: id}
}

// send runs a JSON-RPC request against the currently selected daemon,
// threading through per-cluster dial options. All subcommands should
// use this instead of daemon.SendRequest directly.
func send(req daemon.Request) (*daemon.Response, error) {
	addr, opts := resolveDaemon()
	return daemon.SendRequestWith(addr, req, opts)
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
	root.AddCommand(runCmd())
	root.AddCommand(killCmd())
	root.AddCommand(shiftCmd())
	root.AddCommand(injectCmd())
	root.AddCommand(captureCmd())
	root.AddCommand(versionCmd())
	root.AddCommand(upgradeCmd())
	root.AddCommand(keysCmd())
	root.AddCommand(configCmd())
	root.AddCommand(stopCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func daemonCmd() *cobra.Command {
	var mrvlAddr string
	var listenSocket string
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Start the marvel daemon",
		Long: `Start the marvel daemon. Listens on a Unix socket for local access.
Use --mrvl to also start the mrvl:// listener for remote access.

Examples:
  marvel daemon                              # Unix socket only
  marvel daemon --mrvl                       # + mrvl:// on port 6785
  marvel daemon --mrvl=:7000                 # + mrvl:// on custom port
  marvel daemon --socket /var/marvel.sock    # custom socket path`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sock := listenSocket
			if sock == "" {
				sock = config.DefaultSocket
			}

			d, err := daemon.New()
			if err != nil {
				return err
			}
			if err := d.Start(sock); err != nil {
				return err
			}

			if cmd.Flags().Changed("mrvl") {
				if err := d.StartMRVL(mrvlAddr); err != nil {
					return err
				}
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
	// --mrvl is optional-valued: bare --mrvl enables on the default port,
	// --mrvl=:7000 enables on a custom port. NoOptDefVal prevents cobra
	// from eating the next token when the flag is supplied without =.
	f := cmd.Flags().VarPF(newOptionalString(&mrvlAddr), "mrvl", "",
		"start mrvl:// listener (use --mrvl=:<port> for a custom port)")
	f.NoOptDefVal = ":" + config.DefaultMRVLPort
	cmd.Flags().StringVar(&listenSocket, "socket", "",
		"Unix socket path (default /tmp/marvel.sock)")
	return cmd
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
	var hasRange bool
	cmd := &cobra.Command{
		Use:   "capture <session-key>",
		Short: "Capture a session's pane content",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := map[string]any{"session_key": args[0]}
			if hasRange {
				p["start"] = start
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
	cmd.Flags().IntVarP(&start, "start", "S", 0, "start line (negative for scrollback)")
	cmd.Flags().IntVarP(&end, "end", "E", 0, "end line")
	cmd.PreRun = func(cmd *cobra.Command, args []string) {
		hasRange = cmd.Flags().Changed("start") || cmd.Flags().Changed("end")
	}
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
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade marvel to the latest version",
		Long: `Upgrade marvel to the latest version.

If installed via Homebrew, delegates to brew upgrade.
Otherwise downloads the latest release from GitHub.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return upgrade.Run(channel, targetVersion)
		},
	}
	cmd.Flags().StringVar(&targetVersion, "version", "", "target version (default: latest)")
	return cmd
}

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage marvel cluster configuration",
	}

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
				addr := cl.Socket
				if cl.Server != "" {
					addr = cl.Server
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
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the marvel daemon and clean up all resources",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := send(daemon.Request{Method: "stop"})
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

func renderSessionTable(sessions []api.Session) string {
	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "WORKSPACE\tTEAM\tROLE\tGEN\tNAME\tSTATE\tHEALTH\tCTX%%\tDESK\tAGENT\n")
	for _, s := range sessions {
		agent := s.Runtime.Name
		if agent == "" {
			agent = s.Runtime.Command
		}
		ctx := "-"
		if s.ContextPercent > 0 || !s.LastHeartbeat.IsZero() {
			ctx = fmt.Sprintf("%.0f%%", s.ContextPercent)
		}
		desk := strings.TrimPrefix(s.PaneID, "%")
		gen := fmt.Sprintf("%d", s.Generation)
		health := string(s.HealthState)
		if health == "" {
			health = "unknown"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			s.Workspace, s.Team, s.Role, gen, s.Name, s.State, health, ctx, desk, agent)
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
		fmt.Fprintf(&buf, "    w  workspace      t  team          r  role\n")
		fmt.Fprintf(&buf, "    g  generation     n  name          s  state\n")
		fmt.Fprintf(&buf, "    c  context        d  desk          a  agent\n")
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
			case 'n':
				toggleSort(ws, "name", false)
			case 'r':
				toggleSort(ws, "role", false)
			case 'g':
				toggleSort(ws, "generation", false)
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
