// Command rolescheck applies a manifest to an in-memory store and prints the
// roles the store would hold. It is applycheck cut down to roles, so it builds
// on commits whose team record has no WorkDir and whose workspace has no Root.
//
// Usage:
//
//	rolescheck <manifest>
//
// The manifest is parsed with api.ParseManifestBytes (YAML or TOML) and
// applied to a fresh in-memory store with Manifest.Apply. The output is one
// JSON object keyed by team name, with no workspace prefix:
//
//	{"<team>": {"Roles": [...]}}
//
// An error is printed to standard error with exit status 1 and nothing on
// standard output; a missing manifest argument is a usage error with exit
// status 2. Arguments past the first are ignored.
//
// Two commits serialize role fields differently, so build it from the commit
// the running daemon reports: a checker from another commit
// reads the roles differently and a strict comparison fails closed.
//
// It imports internal/api, so it must be built inside this module.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/arcavenae/marvel/internal/api"
)

var errUsage = errors.New("usage: rolescheck <manifest>")

func run(args []string, w io.Writer) error {
	if len(args) < 1 {
		return errUsage
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	m, err := api.ParseManifestBytes(data)
	if err != nil {
		return err
	}
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
		out[t.Name] = map[string]any{"Roles": team.Roles}
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
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, errUsage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
