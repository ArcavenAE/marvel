package main

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// exampleFiles are the manifests the binary carries, so a quick start works
// from an installed marvel with no source tree.
//
//go:embed examplefiles/*.toml
var exampleFiles embed.FS

// exampleNames lists the embedded manifests by name, sorted.
func exampleNames() []string {
	entries, err := fs.ReadDir(exampleFiles, "examplefiles")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), ".toml"))
	}
	sort.Strings(names)
	return names
}

func exampleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "example [name]",
		Short: "Print a manifest the binary carries",
		Long: `Print an example manifest that is built into marvel, so a first run needs
no source tree. With no name it lists the examples.

  marvel example demo > demo.toml
  marvel work demo.toml

The demo runs two shell loops and needs only tmux. This command is local: it
does not contact a daemon.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				for _, n := range exampleNames() {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), n)
				}
				return nil
			}
			data, err := exampleFiles.ReadFile("examplefiles/" + args[0] + ".toml")
			if err != nil {
				return fmt.Errorf("unknown example %q; known: %s", args[0], strings.Join(exampleNames(), ", "))
			}
			_, err = cmd.OutOrStdout().Write(data)
			return err
		},
	}
}
