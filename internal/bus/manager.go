package bus

import (
	"fmt"
	"path/filepath"
	"sync"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
)

// TeamLister is the slice of the Store the manager reads: which teams are
// applied, and whether one holds a supervisor role.
type TeamLister interface {
	ListTeams() []api.Team
}

// Reloader tells a running broker to re-read authorization. Supervision
// (aae-orc-xy1dh) wires SIGHUP here; until then the field is nil and a
// regenerate only rewrites the files.
type Reloader interface {
	Reload() error
}

// Manager keeps a managed broker's rendered configuration current with the
// applied teams. Team passwords are minted here, held in memory for
// injection into session environments (aae-orc-1qyo3), and written only to
// the 0600 authorization file (D3). They are marvel-issued, mean nothing
// outside this broker, and are revoked by rewrite and reload: issuance under
// ADR-009.
type Manager struct {
	mu          sync.Mutex
	dir         string
	domain      string
	bus         config.ResolvedBus
	teams       TeamLister
	hasLeafSeed func() bool
	admin       User
	passwords   map[string]string // team name -> password

	// Reloader is set by supervision once a broker process exists.
	Reloader Reloader
}

// NewManager builds a manager for a managed bus. dir is where the two files
// live (Layout.StateDir()/nats); domain is the validated cluster name;
// hasLeafSeed reports whether the Store holds the bus/leaf credential.
func NewManager(dir, domain string, rb config.ResolvedBus, teams TeamLister, hasLeafSeed func() bool) (*Manager, error) {
	if !rb.Managed {
		return nil, fmt.Errorf("cluster %s: bus is not managed; nothing to render", domain)
	}
	pw, err := NewPassword()
	if err != nil {
		return nil, err
	}
	return &Manager{
		dir:         dir,
		domain:      domain,
		bus:         rb,
		teams:       teams,
		hasLeafSeed: hasLeafSeed,
		admin:       User{Name: AdminUser, Password: pw},
		passwords:   map[string]string{},
	}, nil
}

// Dir is the directory holding the rendered files.
func (m *Manager) Dir() string { return m.dir }

// ConfPath is the path a broker is started with (-c).
func (m *Manager) ConfPath() string { return filepath.Join(m.dir, ConfName) }

// Domain is the cluster name the broker serves as its JetStream domain.
func (m *Manager) Domain() string { return m.domain }

// Admin returns marvel's own broker identity, for provisioning. Never hand
// it to a session.
func (m *Manager) Admin() User {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.admin
}

// TeamPassword returns the password minted for an applied team, if the team
// has been rendered. The 1qyo3 spawn path injects it as DIRECTOR_NATS_PASS.
func (m *Manager) TeamPassword(team string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pw, ok := m.passwords[team]
	return pw, ok
}

// Regenerate renders both files from the bus section and the applied teams,
// writes them if anything changed, and asks the broker to reload when a
// Reloader is wired. A team keeps its password across regenerations, so a
// running session's credential stays valid; a removed team's password is
// dropped and its user disappears from the file, which is revocation.
func (m *Manager) Regenerate() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	spec := Spec{
		Domain:   m.domain,
		Listen:   m.bus.Listen,
		StoreDir: m.bus.StoreDir,
		HubURL:   m.bus.HubURL,
		LeafSeed: m.hasLeafSeed != nil && m.hasLeafSeed(),
		Admin:    m.admin,
	}
	live := map[string]bool{}
	for _, t := range m.teams.ListTeams() {
		pw, ok := m.passwords[t.Name]
		if !ok {
			var err error
			if pw, err = NewPassword(); err != nil {
				return false, err
			}
			m.passwords[t.Name] = pw
		}
		live[t.Name] = true
		spec.Teams = append(spec.Teams, TeamUser{
			Workspace:  t.Workspace,
			Team:       t.Name,
			Password:   pw,
			Supervisor: hasSupervisorRole(t),
		})
	}
	for name := range m.passwords {
		if !live[name] {
			delete(m.passwords, name)
		}
	}

	conf, err := RenderConf(spec)
	if err != nil {
		return false, fmt.Errorf("render %s: %w", ConfName, err)
	}
	auth, err := RenderAuth(spec)
	if err != nil {
		return false, fmt.Errorf("render %s: %w", AuthName, err)
	}
	changed, err := WriteFiles(m.dir, conf, auth)
	if err != nil {
		return changed, err
	}
	if changed && m.Reloader != nil {
		if err := m.Reloader.Reload(); err != nil {
			return changed, fmt.Errorf("reload broker after rewrite: %w", err)
		}
	}
	return changed, nil
}

// hasSupervisorRole reports whether a team declares the supervisor role, the
// same name internal/team orders last in a shift.
func hasSupervisorRole(t api.Team) bool {
	for _, r := range t.Roles {
		if r.Name == "supervisor" {
			return true
		}
	}
	return false
}
