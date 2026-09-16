package bus

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
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
//
// Passwords survive a daemon restart or reexec. The authorization file
// marvel renders is already the persisted form of every password it has
// minted, at 0600 in its own state directory, so NewManager reads it back
// instead of minting fresh: a running session's team credential stays
// valid at its next reconnect, the adopted-broker reload after a reexec
// is a no-op when nothing changed, and no material is at rest that was
// not already (bus-as-service.md section 8.2; decided in aae-orc-wzexa).
// A team no longer applied still loses its line at the first Regenerate,
// which is the revocation path, unchanged.
type Manager struct {
	mu          sync.Mutex
	dir         string
	domain      string
	bus         config.ResolvedBus
	teams       TeamLister
	hasLeafSeed func() bool
	admin       User
	seat        string            // the director seat's password; empty when no seat is declared
	passwords   map[string]string // team name -> password

	// Reloader is set by supervision once a broker process exists.
	Reloader Reloader
}

// SeatPassName is the file beside the rendered conf that holds the director
// seat's password, 0600, rewritten atomically on every render that mints
// it. The operator's MCP env points DIRECTOR_NATS_PASS_FILE at it once and
// rotation needs no re-paste.
const SeatPassName = "director.pass"

// NewManager builds a manager for a managed bus. dir is where the rendered
// files live (Layout.StateDir()/nats); domain is the validated cluster
// name; hasLeafSeed reports whether the Store holds the bus/leaf
// credential. Passwords already in dir's authorization file are recovered;
// only what is missing is minted.
func NewManager(dir, domain string, rb config.ResolvedBus, teams TeamLister, hasLeafSeed func() bool) (*Manager, error) {
	if !rb.Managed {
		return nil, fmt.Errorf("cluster %s: bus is not managed; nothing to render", domain)
	}
	recovered := RecoverPasswords(filepath.Join(dir, AuthName))
	m := &Manager{
		dir:         dir,
		domain:      domain,
		bus:         rb,
		teams:       teams,
		hasLeafSeed: hasLeafSeed,
		passwords:   map[string]string{},
	}
	pw, ok := recovered[AdminUser]
	if !ok {
		var err error
		if pw, err = NewPassword(); err != nil {
			return nil, err
		}
	}
	m.admin = User{Name: AdminUser, Password: pw}
	if rb.Seat != nil {
		m.seat, ok = recovered[SeatUserName]
		if !ok {
			var err error
			if m.seat, err = NewPassword(); err != nil {
				return nil, err
			}
		}
	}
	for name, pw := range recovered {
		if name == AdminUser || name == SeatUserName {
			continue
		}
		m.passwords[name] = pw
	}
	if n := len(recovered); n > 0 {
		log.Printf("bus: recovered %d password(s) from %s; running sessions keep their credentials", n, filepath.Join(dir, AuthName))
	}
	return m, nil
}

// SeatCredential is the director seat's broker user and password, when the
// cluster declares a seat. It is never injected into a session; it reaches
// the seat through SeatPassPath.
func (m *Manager) SeatCredential() (string, string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seat == "" {
		return "", "", false
	}
	return SeatUserName, m.seat, true
}

// SeatPassPath is where the seat password is written.
func (m *Manager) SeatPassPath() string { return filepath.Join(m.dir, SeatPassName) }

// Bus is the resolved section the manager renders from.
func (m *Manager) Bus() config.ResolvedBus { return m.bus }

// Dir is the directory holding the rendered files.
func (m *Manager) Dir() string { return m.dir }

// ConfPath is the path a broker is started with (-c).
func (m *Manager) ConfPath() string { return filepath.Join(m.dir, ConfName) }

// URL is what sessions receive as NATS_URL.
func (m *Manager) URL() string { return m.bus.URL }

// TeamCredential is the broker user and password for an applied team, the
// session.BusEnv contract. The user is the team name (section 4).
func (m *Manager) TeamCredential(team string) (string, string, bool) {
	pw, ok := m.TeamPassword(team)
	if !ok {
		return "", "", false
	}
	return team, pw, true
}

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
		Domain:    m.domain,
		Listen:    m.bus.Listen,
		StoreDir:  m.bus.StoreDir,
		HubURL:    m.bus.HubURL,
		HubCAFile: m.bus.HubCAFile,
		LeafSeed:  m.hasLeafSeed != nil && m.hasLeafSeed(),
		Admin:     m.admin,
	}
	if m.bus.Seat != nil && m.seat != "" {
		spec.Seat = &SeatUser{Workspace: m.bus.Seat.Workspace, Team: m.bus.Seat.Team, Password: m.seat}
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
	// The seat file rides every render: written when a seat is declared,
	// removed when none is, so a stale secret never outlives its user
	// line. It does not count as a broker change; the broker reads
	// authorization.conf, not this file.
	if err := m.writeSeatPass(spec.Seat); err != nil {
		return changed, err
	}
	if changed && m.Reloader != nil {
		if err := m.Reloader.Reload(); err != nil {
			return changed, fmt.Errorf("reload broker after rewrite: %w", err)
		}
	}
	return changed, nil
}

// writeSeatPass keeps <dir>/director.pass equal to the seat password, or
// absent when there is no seat. Atomic (temp plus rename) so a reader never
// sees a partial value, 0600 because it is a credential.
func (m *Manager) writeSeatPass(seat *SeatUser) error {
	path := m.SeatPassPath()
	if seat == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		return nil
	}
	want := []byte(seat.Password + "\n")
	if have, err := os.ReadFile(path); err == nil && bytes.Equal(have, want) {
		_ = os.Chmod(path, 0o600)
		return nil
	}
	return writeFileAtomic(path, want, 0o600)
}

// writeFileAtomic writes data to a temp file beside path, sets mode, and
// renames it into place.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("rename %s to %s: %w", tmpName, path, err)
	}
	return nil
}

// userLine matches one rendered user entry: `{ user: NAME, password: "PW",`.
// The renderer writes exactly this shape and passwords are URL-safe base64,
// so there is nothing to unescape.
var userLine = regexp.MustCompile(`\{ user: ([A-Za-z0-9_-]+), password: "([^"]*)",`)

// RecoverPasswords reads the passwords back from a rendered authorization
// file, keyed by user. A missing or unreadable file recovers nothing, which
// is the fresh-start case.
func RecoverPasswords(authPath string) map[string]string {
	out := map[string]string{}
	body, err := os.ReadFile(authPath)
	if err != nil {
		return out
	}
	for _, m := range userLine.FindAllStringSubmatch(string(body), -1) {
		if m[2] != "" {
			out[m[1]] = m[2]
		}
	}
	return out
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

// Adopted is the session.BusEnv for a cluster whose bus is managed: false,
// an existing broker marvel did not configure (the phase-0 path). Sessions
// get the URL and no credential; the broker's own authorization, if any,
// is outside marvel's knowledge.
type Adopted struct {
	url string
	bus config.ResolvedBus
}

// NewAdopted returns the provider for an adopted or external broker.
func NewAdopted(rb config.ResolvedBus) Adopted { return Adopted{url: rb.URL, bus: rb} }

// URL is what sessions receive as NATS_URL.
func (a Adopted) URL() string { return a.url }

// Bus is the resolved section, for status.
func (a Adopted) Bus() config.ResolvedBus { return a.bus }

// TeamCredential is never available for an adopted broker.
func (a Adopted) TeamCredential(string) (string, string, bool) { return "", "", false }

// leafEnrolled reports whether a leaf seed is in the store, so a rendered hub
// link can actually come up. Nil hasLeafSeed means never enrolled.
func (m *Manager) leafEnrolled() bool {
	return m.hasLeafSeed != nil && m.hasLeafSeed()
}
