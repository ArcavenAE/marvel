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
//go:embed examplefiles/*.toml examplefiles/*.yaml
var exampleFiles embed.FS

// exampleSuffixes are the file formats an embedded example may be printed in.
var exampleSuffixes = []string{".toml", ".yaml"}

// exampleNames lists the embedded examples by name, sorted, one name per
// example however many formats it ships in.
func exampleNames() []string {
	entries, err := fs.ReadDir(exampleFiles, "examplefiles")
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		n := e.Name()
		for _, suf := range exampleSuffixes {
			n = strings.TrimSuffix(n, suf)
		}
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// exampleFile maps a requested name to the embedded file that holds it. A name
// that ends in a known suffix selects that format; a bare name is the TOML
// file, as it always was.
func exampleFile(name string) string {
	for _, suf := range exampleSuffixes {
		if strings.HasSuffix(name, suf) {
			return "examplefiles/" + name
		}
	}
	return "examplefiles/" + name + ".toml"
}

func exampleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "example [name]",
		Short: "Print a manifest the binary carries",
		Long: `Print an example manifest that is built into marvel, so a first run needs
no source tree. With no name it lists the examples.

  marvel example demo > demo.toml
  marvel work demo.toml

A bare name prints the TOML file. A name ending in .toml or .yaml prints that
format, for an example that ships both:

  marvel example first-cluster.yaml > first-cluster.yaml

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
			data, err := exampleFiles.ReadFile(exampleFile(args[0]))
			if err != nil {
				return fmt.Errorf("unknown example %q; known: %s", args[0], strings.Join(exampleNames(), ", "))
			}
			_, err = cmd.OutOrStdout().Write(data)
			return err
		},
	}
}
