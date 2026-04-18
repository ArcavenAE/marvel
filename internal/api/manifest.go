package api

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// Manifest represents a manifest declaring desired state.
// Supports both YAML (default) and TOML formats.
type Manifest struct {
	Workspace ManifestWorkspace  `toml:"workspace" yaml:"workspace"`
	Teams     []ManifestTeam     `toml:"team"      yaml:"teams"`
	Endpoints []ManifestEndpoint `toml:"endpoint"   yaml:"endpoints"`
}

// ManifestWorkspace is the workspace section of a manifest.
type ManifestWorkspace struct {
	Name string `toml:"name" yaml:"name"`
}

// ManifestTeam is a team section of a manifest.
type ManifestTeam struct {
	Name  string         `toml:"name" yaml:"name"`
	Roles []ManifestRole `toml:"role"  yaml:"roles"`
}

// ManifestRole is a role section within a team.
// Name is the job function. Persona and Identity are the costume and lens.
type ManifestRole struct {
	Name          string               `toml:"name"                    yaml:"name"`
	Replicas      int                  `toml:"replicas"                yaml:"replicas"`
	Runtime       ManifestRuntime      `toml:"runtime"                 yaml:"runtime"`
	RestartPolicy string               `toml:"restart_policy,omitempty" yaml:"restart_policy,omitempty"`
	Permissions   string               `toml:"permissions,omitempty"    yaml:"permissions,omitempty"`
	Persona       string               `toml:"persona,omitempty"        yaml:"persona,omitempty"`
	Identity      string               `toml:"identity,omitempty"       yaml:"identity,omitempty"`
	HealthCheck   *ManifestHealthCheck `toml:"healthcheck,omitempty"    yaml:"healthcheck,omitempty"`
}

// ManifestHealthCheck is the healthcheck section within a role.
type ManifestHealthCheck struct {
	Type             string `toml:"type"                         yaml:"type"`
	Timeout          string `toml:"timeout,omitempty"             yaml:"timeout,omitempty"`
	FailureThreshold int    `toml:"failure_threshold,omitempty"   yaml:"failure_threshold,omitempty"`
}

// ManifestRuntime is the runtime section within a role.
type ManifestRuntime struct {
	Image   string   `toml:"image"          yaml:"image"`
	Command string   `toml:"command"        yaml:"command"`
	Args    []string `toml:"args,omitempty"  yaml:"args,omitempty"`
	Script  string   `toml:"script,omitempty" yaml:"script,omitempty"`
}

// ManifestEndpoint is an endpoint section of a manifest.
type ManifestEndpoint struct {
	Name string `toml:"name" yaml:"name"`
	Team string `toml:"team" yaml:"team"`
}

// ParseManifest reads and parses a manifest file. The format is detected
// from the file extension: .yaml/.yml for YAML, .toml for TOML.
// YAML is the default for ambiguous extensions.
func ParseManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", path, err)
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".toml":
		return parseManifestTOML(data)
	default:
		return parseManifestYAML(data)
	}
}

// ParseManifestBytes parses manifest content. Tries YAML first (default),
// falls back to TOML if YAML parsing fails.
func ParseManifestBytes(data []byte) (*Manifest, error) {
	// Try YAML first — it's the default format.
	m, err := parseManifestYAML(data)
	if err == nil {
		return m, nil
	}

	// Fall back to TOML.
	return parseManifestTOML(data)
}

func parseManifestYAML(data []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse yaml manifest: %w", err)
	}
	return validateManifest(&m)
}

func parseManifestTOML(data []byte) (*Manifest, error) {
	var m Manifest
	if err := toml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse toml manifest: %w", err)
	}
	return validateManifest(&m)
}

func validateManifest(m *Manifest) (*Manifest, error) {
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
	return m, nil
}

// ValidateRuntimes checks that each role's runtime command (and script,
// if set) actually resolves on the daemon's host before the manifest
// is applied. Returns an aggregated error listing every missing binary
// so the operator sees all problems at once — not just the first.
//
// Resolution rules match exec.Command semantics:
//   - Absolute path ("/usr/local/bin/forestage"): os.Stat must succeed.
//   - Path with separator ("bin/simulator", "./scripts/x"): resolved
//     relative to the daemon CWD via os.Stat.
//   - Plain name ("sleep", "forestage"): exec.LookPath searches $PATH.
//   - Empty command: flagged; the manifest parser already catches this
//     but we defend here so misuse of the public API surfaces clearly.
//
// Scripts are checked as absolute/relative paths (never PATH-resolved)
// because scripts are typically repo-relative files, not executables.
//
// See ArcavenAE/marvel#9 / aae-orc-rjm — the pre-fix behavior was to
// silently create panes whose processes exited immediately, hiding the
// real error behind a downstream "can't find pane" warning.
func (m *Manifest) ValidateRuntimes() error {
	var missing []string
	for ti, t := range m.Teams {
		for ri, r := range t.Roles {
			ctx := fmt.Sprintf("team[%d=%s].role[%d=%s]", ti, t.Name, ri, r.Name)
			if err := validateCommand(r.Runtime.Command); err != nil {
				missing = append(missing, fmt.Sprintf("  %s: command %q: %v", ctx, r.Runtime.Command, err))
			}
			if r.Runtime.Script != "" {
				if err := validateScript(r.Runtime.Script); err != nil {
					missing = append(missing, fmt.Sprintf("  %s: script %q: %v", ctx, r.Runtime.Script, err))
				}
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("runtime pre-flight failed on %d role(s):\n%s", len(missing), strings.Join(missing, "\n"))
	}
	return nil
}

func validateCommand(cmd string) error {
	if cmd == "" {
		return errors.New("empty")
	}
	// Path — either absolute or contains a separator — must exist on disk.
	if filepath.IsAbs(cmd) || strings.ContainsRune(cmd, filepath.Separator) {
		if _, err := os.Stat(cmd); err != nil {
			return fmt.Errorf("not found: %w", err)
		}
		return nil
	}
	// Plain name — must resolve on $PATH.
	if _, err := exec.LookPath(cmd); err != nil {
		return fmt.Errorf("not on PATH: %w", err)
	}
	return nil
}

func validateScript(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("not found: %w", err)
	}
	return nil
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
			role := Role{
				Name:          mr.Name,
				Replicas:      mr.Replicas,
				Runtime:       rt,
				RestartPolicy: RestartAlways,
				Permissions:   mr.Permissions,
				Persona:       mr.Persona,
				Identity:      mr.Identity,
			}
			if mr.RestartPolicy != "" {
				role.RestartPolicy = RestartPolicy(mr.RestartPolicy)
			}
			if mr.HealthCheck != nil {
				timeout := 30 * time.Second
				if mr.HealthCheck.Timeout != "" {
					d, err := time.ParseDuration(mr.HealthCheck.Timeout)
					if err != nil {
						return fmt.Errorf("parse healthcheck timeout %q: %w", mr.HealthCheck.Timeout, err)
					}
					timeout = d
				}
				threshold := 3
				if mr.HealthCheck.FailureThreshold > 0 {
					threshold = mr.HealthCheck.FailureThreshold
				}
				role.HealthCheck = &HealthCheck{
					Type:             HealthCheckType(mr.HealthCheck.Type),
					Timeout:          timeout,
					FailureThreshold: threshold,
				}
			}
			roles = append(roles, role)
		}

		team := &Team{
			Name:       mt.Name,
			Workspace:  m.Workspace.Name,
			Roles:      roles,
			Generation: 1,
			CreatedAt:  now,
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
