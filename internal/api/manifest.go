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
	Name     string          `toml:"name"`
	Replicas int             `toml:"replicas"`
	Runtime  ManifestRuntime `toml:"runtime"`
	Role     string          `toml:"role,omitempty"`
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
		if t.Replicas < 1 {
			return nil, fmt.Errorf("parse manifest: team[%d].replicas must be >= 1", i)
		}
		if t.Runtime.Image == "" && t.Runtime.Command == "" {
			return nil, fmt.Errorf("parse manifest: team[%d].runtime needs image or command", i)
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
		rt := Runtime{
			Name:    mt.Runtime.Image,
			Command: mt.Runtime.Command,
			Args:    mt.Runtime.Args,
			Script:  mt.Runtime.Script,
		}
		if rt.Name == "" {
			rt.Name = mt.Runtime.Command
		}

		team := &Team{
			Name:      mt.Name,
			Workspace: m.Workspace.Name,
			Replicas:  mt.Replicas,
			Runtime:   rt,
			Role:      mt.Role,
			CreatedAt: now,
		}
		// Update replicas if team already exists.
		existing, err := store.GetTeam(team.Key())
		if err == nil {
			existing.Replicas = mt.Replicas
			existing.Runtime = rt
			existing.Role = mt.Role
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
