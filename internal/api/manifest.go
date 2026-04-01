package api

import (
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
)

// Manifest represents a TOML manifest declaring desired state.
type Manifest struct {
	Workspace ManifestWorkspace `toml:"workspace"`
	Teams     []ManifestTeam    `toml:"team"`
	Endpoints []ManifestEndpoint `toml:"endpoint"`
}

// ManifestWorkspace is the workspace section of a manifest.
type ManifestWorkspace struct {
	Name string `toml:"name"`
}

// ManifestTeam is a team section of a manifest.
type ManifestTeam struct {
	Name  string         `toml:"name"`
	Roles []ManifestRole `toml:"role"`
}

// ManifestRole is a role section within a team.
type ManifestRole struct {
	Name     string          `toml:"name"`
	Replicas int             `toml:"replicas"`
	Runtime  ManifestRuntime `toml:"runtime"`
}

// ManifestRuntime is the runtime section within a team.
type ManifestRuntime struct {
	Image   string   `toml:"image"`
	Command string   `toml:"command"`
	Args    []string `toml:"args,omitempty"`
	Script  string   `toml:"script,omitempty"`
}

// ManifestEndpoint is an endpoint section of a manifest.
type ManifestEndpoint struct {
	Name string `toml:"name"`
	Team string `toml:"team"`
}

// ParseManifest reads and parses a TOML manifest file.
func ParseManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", path, err)
	}
	return ParseManifestBytes(data)
}

// ParseManifestBytes parses TOML manifest content.
func ParseManifestBytes(data []byte) (*Manifest, error) {
	var m Manifest
	if err := toml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if m.Workspace.Name == "" {
		return nil, fmt.Errorf("parse manifest: workspace.name is required")
	}
	for i, t := range m.Teams {
		if t.Name == "" {
			return nil, fmt.Errorf("parse manifest: team[%d].name is required", i)
		}
		if len(t.Roles) == 0 {
			return nil, fmt.Errorf("parse manifest: team[%d] must have at least one role", i)
		}
		for j, r := range t.Roles {
			if r.Name == "" {
				return nil, fmt.Errorf("parse manifest: team[%d].role[%d].name is required", i, j)
			}
			if r.Replicas < 1 {
				return nil, fmt.Errorf("parse manifest: team[%d].role[%d].replicas must be >= 1", i, j)
			}
			if r.Runtime.Image == "" && r.Runtime.Command == "" {
				return nil, fmt.Errorf("parse manifest: team[%d].role[%d].runtime needs image or command", i, j)
			}
		}
	}
	return &m, nil
}

// Apply converts a manifest into store resources and creates them.
func (m *Manifest) Apply(store *Store) error {
	now := time.Now().UTC()

	ws := &Workspace{Name: m.Workspace.Name, CreatedAt: now}
	// Ignore already-exists for workspace (idempotent apply).
	if err := store.CreateWorkspace(ws); err != nil && !isAlreadyExists(err) {
		return fmt.Errorf("apply workspace: %w", err)
	}

	for _, mt := range m.Teams {
		var roles []Role
		for _, mr := range mt.Roles {
			rt := Runtime{
				Name:    mr.Runtime.Image,
				Command: mr.Runtime.Command,
				Args:    mr.Runtime.Args,
				Script:  mr.Runtime.Script,
			}
			if rt.Name == "" {
				rt.Name = rt.Command
			}
			roles = append(roles, Role{
				Name:     mr.Name,
				Replicas: mr.Replicas,
				Runtime:  rt,
			})
		}

		team := &Team{
			Name:      mt.Name,
			Workspace: m.Workspace.Name,
			Roles:     roles,
			CreatedAt: now,
		}
		// Update roles if team already exists.
		existing, err := store.GetTeam(team.Key())
		if err == nil {
			existing.Roles = roles
		} else {
			if err := store.CreateTeam(team); err != nil {
				return fmt.Errorf("apply team %s: %w", mt.Name, err)
			}
		}
	}

	for _, me := range m.Endpoints {
		ep := &Endpoint{
			Name:      me.Name,
			Workspace: m.Workspace.Name,
			Team:      me.Team,
		}
		if err := store.CreateEndpoint(ep); err != nil && !isAlreadyExists(err) {
			return fmt.Errorf("apply endpoint %s: %w", me.Name, err)
		}
	}

	return nil
}

func isAlreadyExists(err error) bool {
	return err != nil && err.Error() != "" && contains(err.Error(), "already exists")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
