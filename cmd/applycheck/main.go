// Command applycheck applies a manifest to an in-memory store and prints what
// the store would hold, so an upgrade's post-check can compare it with a
// reference without touching a daemon.
//
// Usage:
//
//	applycheck <manifest> <workspace-root>
//
// The manifest is parsed with api.ParseManifestBytes (YAML or TOML), its
// Workspace.Root is set to the second argument, and it is applied to a fresh
// in-memory store with Manifest.Apply. The output is one JSON object keyed by
// team name, with no workspace prefix:
//
//	{"<team>": {"WorkDir": ..., "Roles": [...], "Budget": ...}}
//
// A team that declares no workdir is anchored at the root argument, which is
// the move a workdirs upgrade checks for. Arguments past the second are
// ignored. Any error panics, so the exit status
// is nonzero and standard output stays empty (the panic text goes to standard
// error); the caller reads that as "does not parse or apply".
//
// It imports internal/api, so it must be built inside this module, from the
// commit whose apply behavior the comparison is meant to reflect.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/arcavenae/marvel/internal/api"
)

func run(args []string, w io.Writer) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: applycheck <manifest> <workspace-root>")
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	m, err := api.ParseManifestBytes(data)
	if err != nil {
		return err
	}
	m.Workspace.Root = args[1]
	s := api.NewStore()
	if err := m.Apply(s); err != nil {
		return err
	}
	out := map[string]any{}
	for _, t := range m.Teams {
		team, err := s.GetTeam(m.Workspace.Name + "/" + t.Name)
		if err != nil {
			return err
		}
		out[t.Name] = map[string]any{"WorkDir": team.WorkDir, "Roles": team.Roles, "Budget": team.Budget}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		panic(err)
	}
}
