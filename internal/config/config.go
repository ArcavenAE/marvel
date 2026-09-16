// Package config manages marvel's client configuration, modeled after
// kubeconfig. Stores cluster connection details in ~/.marvel/config.yaml.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/arcavenae/marvel/internal/paths"
	"github.com/arcavenae/marvel/internal/service"
)

const (
	// DefaultMRVLPort is the default port for the mrvl:// protocol.
	// Mnemonic: MRVL on phone keypad (M=6, R=7, V=8, L=5).
	DefaultMRVLPort = "6785"

	// SocketEnv overrides the default socket path for both the daemon
	// and the client.
	//
	// The name is not new. Marvel already injects MARVEL_SOCKET into the
	// environment of every agent it spawns (runtime/adapter.go,
	// session/manager.go) and reads it in `marvel ctx-forward`. Until
	// this seam was finished, marvel told every agent where the socket
	// was and then ignored the answer in its own CLI.
	//
	// It exists for the cases the layout cannot serve: an NFS home, where
	// Unix sockets do not work at all, and socket activation, which wants
	// /run. See docs/design/daemon-isolation.md decision 3.
	SocketEnv = "MARVEL_SOCKET"

	// LegacySocket is the machine-global path marvel defaulted to before
	// the socket moved into the layout. Retained only so a config.yaml
	// still pinning it can be recognised and warned about; nothing
	// resolves to it. See aae-orc-t6da.
	LegacySocket = "/tmp/marvel.sock"
)

// DefaultSocket returns the default control socket path,
// ~/.marvel/run/marvel.sock, resolved through the paths layout.
//
// It is a function rather than a constant because the layout is rooted
// at the user's home directory. Two daemons under different homes get
// different sockets, which is the property the previous hardcoded
// constant destroyed: it bypassed a layout that every other runtime
// artifact honors, so daemons isolated by HOME collided anyway.
//
// Falls back to the legacy path only when the home directory cannot be
// determined at all, which is the same condition under which nothing
// else in the layout would work either.
func DefaultSocket() string {
	layout, err := paths.Default()
	if err != nil {
		return LegacySocket
	}
	return layout.RuntimeSocket()
}

// ResolveSocket returns the socket address to use when no --socket flag
// and no cluster entry apply: the MARVEL_SOCKET environment override if
// set, otherwise the layout default.
func ResolveSocket() string {
	if s := os.Getenv(SocketEnv); s != "" {
		return s
	}
	return DefaultSocket()
}

// LegacySocketWarning returns a warning to show the operator when the
// address they are about to use is the old machine-global default,
// or the empty string when there is nothing to warn about.
//
// A stale config that quietly keeps the old behavior reproduces the bug
// the move closes, and the dangerous branch is not the obvious one.
// Measured 2026-08-06: a stale path with nothing listening already fails
// loudly with a connect error and exit 1, but a stale path with ANOTHER
// daemon on it returns a well-formed, successful, empty result with exit
// code 0, and a mutating call reports success against the wrong daemon.
// The path alone cannot distinguish those, so the operator gets told
// rather than guessing.
//
// This warns. It does not rewrite the operator's config.
func LegacySocketWarning(addr string) string {
	if addr != LegacySocket {
		return ""
	}
	return fmt.Sprintf(
		"warning: using the legacy machine-global socket %s; "+
			"the default is now %s. Two daemons can share the legacy path, "+
			"and a client that reaches the wrong one gets an empty answer with "+
			"exit code 0. Update config.yaml, or set %s to silence this.",
		LegacySocket, DefaultSocket(), SocketEnv)
}

// ClientHome returns the layout home this client resolves against,
// ~/.marvel, or the empty string when the home directory cannot be
// determined. It is the expectation a daemon's self-reported home is
// compared with.
func ClientHome() string {
	layout, err := paths.Default()
	if err != nil {
		return ""
	}
	return layout.Home
}

// DaemonHomeWarning returns a warning to show the operator when the
// daemon that answered is rooted at a different layout home than the
// client resolved against, or the empty string when there is nothing to
// warn about.
//
// This is what makes a wrong-daemon hit visible at all. Measured
// 2026-08-06: a client that reaches the wrong daemon gets a well-formed,
// successful, EMPTY answer with exit code 0, and a mutating call reports
// success against the wrong daemon. Neither the path nor the exit code
// distinguishes that from a correct call, so the daemon says which
// layout it is rooted at and the client checks.
//
// It warns. It does not fail the command, and it cannot prevent
// anything: the field arrives on the response, so a mutating call has
// already been carried out by the time this runs. See
// docs/design/daemon-isolation.md decision 8 and aae-orc-sqh0.
//
// Silent by design in three cases:
//
//   - Remote addresses (mrvl://, ssh://, tcp://, host:port). A remote
//     daemon runs under its own home and is EXPECTED to differ; warning
//     there would be noise on every single remote call.
//   - An empty reported home, which is a daemon predating this field.
//   - An empty client home, which leaves nothing to compare against.
func DaemonHomeWarning(addr, daemonHome, clientHome string) string {
	if daemonHome == "" || clientHome == "" {
		return ""
	}
	if !isLocalSocket(addr) {
		return ""
	}
	if daemonHome == clientHome {
		return ""
	}
	return fmt.Sprintf(
		"warning: the daemon at %s is rooted at %s, but this client resolved against %s. "+
			"The answer above came from a different marvel. "+
			"Pass --socket, or set %s, to reach the one you meant.",
		addr, daemonHome, clientHome, SocketEnv)
}

// isLocalSocket reports whether addr names a Unix socket on this
// machine. A scheme means remote; otherwise the host:port rule decides,
// and that rule is paths.IsTCPAddr so the client cannot drift from what
// the daemon and the dialer do.
//
// The scheme tests are kept explicit rather than left to the colon in
// "://", so that adding a fourth scheme is a change here and not a
// coincidence of punctuation.
func isLocalSocket(addr string) bool {
	if isMRVL(addr) || isSSH(addr) || strings.HasPrefix(addr, "tcp://") {
		return false
	}
	return !paths.IsTCPAddr(addr)
}

// Config is the top-level marvel client configuration.
type Config struct {
	Clusters       []Cluster `yaml:"clusters"`
	CurrentCluster string    `yaml:"current_cluster"`
}

// Cluster defines how to connect to a marvel daemon.
type Cluster struct {
	Name     string `yaml:"name"`
	Socket   string `yaml:"socket,omitempty"`   // Unix socket path (local)
	Server   string `yaml:"server,omitempty"`   // mrvl://user@host[:port] (remote)
	Identity string `yaml:"identity,omitempty"` // client private key for this cluster
	// Bus is the cluster's local NATS broker, when it has one. Topology and
	// credentials live here on the daemon's host, never in a workspace
	// manifest (brief 10 section 3, candidate R-96).
	//
	// Bus is sugar: the cluster's services are the Services list, and a
	// bus: block is read as the entry named "bus" of class message-bus and
	// provider nats-server (docs/design/services-list.md section 1.3).
	// Declaring both a bus: block and a services: entry of that class is
	// ErrInvalidService, because the two spellings would name one broker
	// twice. Neither spelling is rewritten into the other.
	Bus *Bus `yaml:"bus,omitempty"`
	// Services is the cluster-level list of services marvel runs, adopts,
	// or reaches (docs/design/services-list.md). Each entry carries the
	// four common fields and a provider body decoded by the driver.
	Services []Service `yaml:"services,omitempty"`
}

// Service is one entry of a cluster's Services list. The four common
// fields are validated against the registries in internal/service; the
// rest of the mapping is the provider body, kept as parsed so the driver
// decodes it (BusSpec for nats-server) and so Save writes it back as it
// was read.
type Service struct {
	// Name is unique per cluster. The lifted bus: block is named "bus".
	Name string `yaml:"name"`
	// Class is the interface a consumer depends on; registered classes
	// only (service.Class).
	Class string `yaml:"class"`
	// Provider is the driver of the class; registered providers only, and
	// the provider must drive this class (service.Check).
	Provider string `yaml:"provider"`
	// Mode is managed, adopted, or external (service.Mode); "internal" is
	// reserved and refused.
	Mode string `yaml:"mode"`
	// CallerIdentity is what a consumer presents, from the class
	// contract's set. Parsed here; the structural check against the class
	// set and the driver's Delivers, and single-value defaulting, land
	// under aae-orc-chwhk.
	CallerIdentity string `yaml:"caller_identity,omitempty"`
	// URL is what consumers receive.
	URL string `yaml:"url,omitempty"`
	// CAFile trusts the service's own listener when it is TLS.
	CAFile string `yaml:"ca_file,omitempty"`

	// body is the whole mapping as parsed, for the provider to decode its
	// own keys from and for Save to round-trip. Nil for an entry built in
	// code, which then carries its provider body in bus.
	body *yaml.Node
	// bus is the provider body of a lifted bus: block.
	bus *Bus
}

// UnmarshalYAML decodes the common fields and keeps the mapping, so the
// provider body survives without a schema here for every driver's keys.
func (s *Service) UnmarshalYAML(n *yaml.Node) error {
	type plain Service
	var p plain
	if err := n.Decode(&p); err != nil {
		return err
	}
	*s = Service(p)
	s.body = n
	return nil
}

// MarshalYAML writes the entry back as it was read when it came from a
// file, so a Save after a cluster add does not strip the provider body.
func (s Service) MarshalYAML() (any, error) {
	if s.body != nil {
		return s.body, nil
	}
	type plain Service
	return plain(s), nil
}

// BusSpec is the nats-server provider's body: the Bus fields, decoded
// from the entry's mapping (or carried whole from a lifted bus: block).
// Callers check Provider first; a body decoded for the wrong driver is
// meaningless, not an error.
func (s *Service) BusSpec() (*Bus, error) {
	if s.bus != nil {
		return s.bus, nil
	}
	if s.body == nil {
		return nil, fmt.Errorf("service %q has no provider body", s.Name)
	}
	var b Bus
	if err := s.body.Decode(&b); err != nil {
		return nil, fmt.Errorf("service %q: decode %s body: %w", s.Name, s.Provider, err)
	}
	// The entry's common fields are the record; a duplicate inside the
	// body must agree with them.
	if b.Mode == "" {
		b.Mode = s.Mode
	}
	if b.Class == "" {
		b.Class = s.Class
	}
	if b.Provider == "" {
		b.Provider = s.Provider
	}
	return &b, nil
}

// BusServiceName is the name the lifted bus: block takes in the Services
// list, and the name marvel bus status projects.
const BusServiceName = "bus"

// ErrInvalidService marks a Services entry the registries refuse or a list
// that names one service twice. Callers match it with errors.Is; like
// ErrInvalidBus it rides beside the parsed config.
var ErrInvalidService = errors.New("invalid service entry")

// AllServices returns the cluster's Services list with the bus: block
// lifted in as the entry named "bus". The lift is a read-time view: the
// config file keeps whichever spelling it had. A cluster declaring both a
// bus: block and a message-bus entry is refused, since the two would name
// one broker twice.
func (c *Cluster) AllServices() ([]Service, error) {
	out := append([]Service(nil), c.Services...)
	if c.Bus == nil {
		return out, nil
	}
	for _, s := range c.Services {
		if s.Class == service.ClassMessageBus || s.Name == BusServiceName {
			return nil, fmt.Errorf("%w: cluster %q declares both a bus: block and services entry %q of class %s; keep one spelling", ErrInvalidService, c.Name, s.Name, service.ClassMessageBus)
		}
	}
	lifted, err := c.Bus.lift()
	if err != nil {
		return nil, fmt.Errorf("%w: cluster %q: bus: %w", ErrInvalidService, c.Name, err)
	}
	return append(out, lifted), nil
}

// BusService returns the cluster's message-bus entry, from either
// spelling, or nil when the cluster has none.
func (c *Cluster) BusService() (*Service, error) {
	all, err := c.AllServices()
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].Class == service.ClassMessageBus {
			return &all[i], nil
		}
	}
	return nil, nil
}

// ValidateServices checks a cluster's Services list, both spellings: names
// unique, class and provider registered and paired, mode one of the three,
// and each nats-server body valid under ValidateBus. Every refusal is
// structural (ADR-007 clause 3).
func ValidateServices(c *Cluster) error {
	all, err := c.AllServices()
	if err != nil {
		return err
	}
	var errs []error
	seen := map[string]bool{}
	for i := range all {
		s := &all[i]
		if s.Name == "" {
			errs = append(errs, fmt.Errorf("%w: cluster %q: services[%d] has no name", ErrInvalidService, c.Name, i))
			continue
		}
		if seen[s.Name] {
			errs = append(errs, fmt.Errorf("%w: cluster %q: service %q is declared twice", ErrInvalidService, c.Name, s.Name))
			continue
		}
		seen[s.Name] = true
		mode, merr := service.ParseMode(s.Mode)
		if merr != nil {
			errs = append(errs, fmt.Errorf("%w: cluster %q: service %q: %w", ErrInvalidService, c.Name, s.Name, merr))
			continue
		}
		if _, _, cerr := service.Check(s.Class, s.Provider, mode); cerr != nil {
			errs = append(errs, fmt.Errorf("%w: cluster %q: service %q: %w", ErrInvalidService, c.Name, s.Name, cerr))
			continue
		}
		if s.Provider == service.ProviderNATSServer {
			b, berr := s.BusSpec()
			if berr != nil {
				errs = append(errs, fmt.Errorf("%w: cluster %q: service %q: %w", ErrInvalidService, c.Name, s.Name, berr))
				continue
			}
			if verr := ValidateBus(c.Name, b); verr != nil {
				errs = append(errs, verr)
			}
		}
	}
	return errors.Join(errs...)
}

// Bus describes a cluster's local broker. There is no credential path (the
// leaf seed is the Store's transient bus/leaf credential and reaches the
// broker through its environment) and no CA field yet (TLS is deferred under
// the interim LAN posture); both are added when their feature is.
type Bus struct {
	// Class and Provider are the Services record's common fields, optional
	// inside a bus: block where they can only be message-bus and
	// nats-server; any other value is refused.
	Class    string `yaml:"class,omitempty"`
	Provider string `yaml:"provider,omitempty"`
	// Mode is managed, adopted, or external (service.Mode). Empty derives
	// from Managed: true is managed, false with a url is adopted. When both
	// are present mode wins and a disagreement is refused. "internal" is
	// reserved by comment in internal/service and refused.
	//
	// ADR-009 reads through this field. Marvel-held secrets for a bus are
	// issuance only while the broker is marvel-supervised, which is mode
	// managed: the daemon mints the admin, seat, and team passwords, can
	// rewrite and reload them, and no third party's console is involved.
	// An adopted or external record carries no marvel-minted secret, and
	// has no field for a foreign one on purpose: holding a credential for
	// a broker marvel did not configure would be custody. The one
	// credential marvel holds for an external broker is the hub leaf
	// seed, ruled separately (the hub operator mints, marvel distributes).
	// Moving a managed broker out of marvel's supervision is the ADR-009
	// tripwire; change the mode and the secrets go with it.
	Mode string `yaml:"mode,omitempty"`
	// CallerIdentity is the Services record's field; see Service.
	CallerIdentity string `yaml:"caller_identity,omitempty"`
	// Managed true means marvel renders the conf and supervises the broker;
	// false adopts an existing broker at URL (the phase-0 path). It is the
	// spelling brief 10's examples use and keeps parsing; Mode is the
	// record.
	Managed bool `yaml:"managed"`
	// managedSet records whether the managed key was present, so a
	// disagreement with mode can be told from an absent bool.
	managedSet bool
	// Listen is the explicit host:port the broker binds. It is never derived
	// from the HOME tag: two daemons in one HOME would derive the same port.
	Listen string `yaml:"listen,omitempty"`
	// URL is what sessions receive as NATS_URL. Defaults to nats://<listen>.
	URL string `yaml:"url,omitempty"`
	// StoreDir is the JetStream store root. Defaults to <StateDir>/nats.
	StoreDir string `yaml:"store_dir,omitempty"`
	// Seat is the human director seat's home subtree. The seat is not a
	// marvel session, so marvel cannot know it; declaring it renders a
	// broker user named director confined to that subtree (the R-95 seat
	// row) and writes its password to <StateDir>/nats/director.pass.
	// Absent, no director user renders. Only meaningful under mode
	// managed.
	Seat *Seat `yaml:"seat,omitempty"`
	// Hub, when set, makes the broker a leaf of the global tier.
	Hub *Hub `yaml:"hub,omitempty"`
}

// Seat names the workspace and team the director seat lives in, both
// subject tokens (R-76). The team names the broker user's subtree, not a
// user: the user is always director.
type Seat struct {
	Workspace string `yaml:"workspace"`
	Team      string `yaml:"team"`
}

// Hub is the global tier a managed broker joins as a leaf: a Service
// record of its own with mode external implied. Its health is the leaf
// link, which the supervisor polls on /leafz every 30s and reports as
// bus.leaf.up and bus.leaf.down.
type Hub struct {
	URL string `yaml:"url"`
	// CAFile trusts the hub's TLS listener from the rendered leaf remote
	// (aae-orc-i9i23). Empty for a plaintext hub.
	CAFile string `yaml:"ca_file,omitempty"`
}

// ReservedBusUsers are broker user names the renderer owns; a team or
// seat by either name is refused, never merged.
var ReservedBusUsers = []string{"director", "marvel_admin"}

// validSubjectToken checks a name against the R-76 class [A-Za-z0-9_-],
// naming what the name is for in the error.
func validSubjectToken(kind, s string) error {
	if s == "" {
		return fmt.Errorf("%s is empty", kind)
	}
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-'
		if !ok {
			return fmt.Errorf("%s %q contains %q; the class is [A-Za-z0-9_-] and a name is rejected, not rewritten (R-76)", kind, s, string(r))
		}
	}
	return nil
}

// UnmarshalYAML decodes the block and notes whether managed was written,
// which the mode derivation needs and a bool cannot carry.
func (b *Bus) UnmarshalYAML(n *yaml.Node) error {
	type plain Bus
	var p plain
	if err := n.Decode(&p); err != nil {
		return err
	}
	*b = Bus(p)
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == "managed" {
				b.managedSet = true
			}
		}
	}
	return nil
}

// EffectiveMode resolves mode from the two spellings: an explicit mode
// wins; otherwise managed: true is managed and anything else is adopted.
// An explicit mode that disagrees with a written managed bool is refused,
// as is the reserved word or an unknown value.
func (b *Bus) EffectiveMode() (service.Mode, error) {
	if b.Mode == "" {
		if b.Managed {
			return service.ModeManaged, nil
		}
		return service.ModeAdopted, nil
	}
	mode, err := service.ParseMode(b.Mode)
	if err != nil {
		return "", err
	}
	if b.managedSet {
		if b.Managed && mode != service.ModeManaged {
			return "", fmt.Errorf("managed: true disagrees with mode: %s", mode)
		}
		if !b.Managed && mode == service.ModeManaged {
			return "", fmt.Errorf("managed: false disagrees with mode: managed")
		}
	}
	return mode, nil
}

// lift turns a bus: block into the Services entry named "bus". The class
// and provider are implied by the spelling; a value written inside the
// block must agree.
func (b *Bus) lift() (Service, error) {
	if b.Class != "" && b.Class != service.ClassMessageBus {
		return Service{}, fmt.Errorf("class %q is not %s; a bus: block can only be the message bus", b.Class, service.ClassMessageBus)
	}
	if b.Provider != "" && b.Provider != service.ProviderNATSServer {
		return Service{}, fmt.Errorf("provider %q is not %s; a bus: block can only be the nats-server driver", b.Provider, service.ProviderNATSServer)
	}
	mode, err := b.EffectiveMode()
	if err != nil {
		return Service{}, err
	}
	return Service{
		Name:           BusServiceName,
		Class:          service.ClassMessageBus,
		Provider:       service.ProviderNATSServer,
		Mode:           string(mode),
		CallerIdentity: b.CallerIdentity,
		URL:            b.URL,
		bus:            b,
	}, nil
}

// ErrInvalidBus marks a cluster whose bus section is unusable. Callers
// match it with errors.Is; like ErrInvalidClusterName it rides beside the
// parsed config so tolerant callers can proceed.
var ErrInvalidBus = errors.New("invalid bus section")

// ValidateBus checks a bus section. A managed broker needs an explicit
// listen host:port; a url or hub url must parse with a nats scheme.
func ValidateBus(cluster string, b *Bus) error {
	if b == nil {
		return nil
	}
	var errs []error
	mode, merr := b.EffectiveMode()
	if merr != nil {
		return fmt.Errorf("%w: cluster %q: %w", ErrInvalidBus, cluster, merr)
	}
	managed := mode == service.ModeManaged
	if b.Class != "" && b.Class != service.ClassMessageBus {
		errs = append(errs, fmt.Errorf("%w: cluster %q: class %q is not %s", ErrInvalidBus, cluster, b.Class, service.ClassMessageBus))
	}
	if b.Provider != "" && b.Provider != service.ProviderNATSServer {
		errs = append(errs, fmt.Errorf("%w: cluster %q: provider %q is not %s", ErrInvalidBus, cluster, b.Provider, service.ProviderNATSServer))
	}
	if managed && b.Listen == "" {
		errs = append(errs, fmt.Errorf("%w: cluster %q: managed bus needs an explicit listen host:port (no derivation from the socket or HOME)", ErrInvalidBus, cluster))
	}
	if b.Listen != "" {
		host, port, err := net.SplitHostPort(b.Listen)
		if err != nil {
			errs = append(errs, fmt.Errorf("%w: cluster %q: listen %q is not host:port: %v", ErrInvalidBus, cluster, b.Listen, err))
		} else {
			if host == "" {
				errs = append(errs, fmt.Errorf("%w: cluster %q: listen %q needs an explicit host", ErrInvalidBus, cluster, b.Listen))
			}
			if n, perr := strconv.Atoi(port); perr != nil || n < 1 || n > 65535 {
				errs = append(errs, fmt.Errorf("%w: cluster %q: listen %q has a bad port", ErrInvalidBus, cluster, b.Listen))
			}
		}
	}
	if !managed && b.Listen == "" && b.URL == "" {
		errs = append(errs, fmt.Errorf("%w: cluster %q: an %s bus needs url", ErrInvalidBus, cluster, mode))
	}
	if b.URL != "" {
		if err := checkNATSURL(b.URL, "nats", "tls"); err != nil {
			errs = append(errs, fmt.Errorf("%w: cluster %q: url: %v", ErrInvalidBus, cluster, err))
		}
	}
	if b.Hub != nil {
		if err := checkNATSURL(b.Hub.URL, "nats-leaf", "nats", "tls"); err != nil {
			errs = append(errs, fmt.Errorf("%w: cluster %q: hub.url: %v", ErrInvalidBus, cluster, err))
		}
		if b.Hub.CAFile != "" && !filepath.IsAbs(cleanPath(b.Hub.CAFile)) {
			errs = append(errs, fmt.Errorf("%w: cluster %q: hub.ca_file %q must be an absolute path (or ~-relative); the broker resolves it, not the daemon", ErrInvalidBus, cluster, b.Hub.CAFile))
		}
	}
	if b.Seat != nil {
		if !managed {
			errs = append(errs, fmt.Errorf("%w: cluster %q: seat renders a director user, which only a managed bus can carry; this bus is %s", ErrInvalidBus, cluster, mode))
		}
		if err := validSubjectToken("seat.workspace", b.Seat.Workspace); err != nil {
			errs = append(errs, fmt.Errorf("%w: cluster %q: %v", ErrInvalidBus, cluster, err))
		}
		if err := validSubjectToken("seat.team", b.Seat.Team); err != nil {
			errs = append(errs, fmt.Errorf("%w: cluster %q: %v", ErrInvalidBus, cluster, err))
		}
		for _, r := range ReservedBusUsers {
			if b.Seat.Team == r {
				errs = append(errs, fmt.Errorf("%w: cluster %q: seat.team %q is a reserved broker user name", ErrInvalidBus, cluster, r))
			}
		}
	}
	return errors.Join(errs...)
}

func checkNATSURL(raw string, schemes ...string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%q does not parse: %v", raw, err)
	}
	for _, sch := range schemes {
		if u.Scheme == sch {
			if u.Host == "" {
				return fmt.Errorf("%q has no host", raw)
			}
			return nil
		}
	}
	return fmt.Errorf("%q scheme %q is not one of %v", raw, u.Scheme, schemes)
}

// ResolvedBus is a bus section with its defaults filled in. Defaults are
// applied here, at read time, never written back into the config file.
type ResolvedBus struct {
	// Class and Provider are filled to the bus defaults when the block
	// left them implicit; CallerIdentity is carried as declared (its
	// defaulting is aae-orc-chwhk).
	Class          string
	Provider       string
	CallerIdentity string
	// Mode is the effective service.Mode; Managed is true exactly when it
	// is managed, kept for the callers that only ask that.
	Mode     service.Mode
	Managed  bool
	Listen   string
	URL      string
	StoreDir string
	HubURL   string
	// HubCAFile is ~-expanded, since the broker process reads it.
	HubCAFile string
	// Seat is copied through when declared.
	Seat *Seat
}

// Resolve fills the bus defaults: url from listen, store_dir under
// stateDir/nats. stateDir is the daemon's Layout.StateDir().
func (b *Bus) Resolve(stateDir string) ResolvedBus {
	mode, err := b.EffectiveMode()
	if err != nil {
		// Resolve does not validate; a section ValidateBus refused resolves
		// as adopted so callers that skipped validation still get a URL.
		mode = service.ModeAdopted
	}
	r := ResolvedBus{
		Class: b.Class, Provider: b.Provider, CallerIdentity: b.CallerIdentity,
		Mode: mode, Managed: mode == service.ModeManaged,
		Listen: b.Listen, URL: b.URL, StoreDir: b.StoreDir, Seat: b.Seat,
	}
	if r.Class == "" {
		r.Class = service.ClassMessageBus
	}
	if r.Provider == "" {
		r.Provider = service.ProviderNATSServer
	}
	if r.URL == "" && r.Listen != "" {
		r.URL = "nats://" + r.Listen
	}
	if r.StoreDir == "" {
		r.StoreDir = filepath.Join(stateDir, "nats")
	}
	if b.Hub != nil {
		r.HubURL = b.Hub.URL
		if b.Hub.CAFile != "" {
			r.HubCAFile = cleanPath(b.Hub.CAFile)
		}
	}
	return r
}

// ClusterForSocket returns the cluster entry whose socket is the daemon's
// own listen path, or nil. A cluster with neither socket nor server is the
// default local socket. Paths are ~-expanded and cleaned before comparing;
// nothing is rewritten. This is how a daemon finds its bus section: the
// client config names clusters by how to reach them, and the daemon is
// reached at exactly one path.
func (c *Config) ClusterForSocket(socket string) *Cluster {
	want := cleanPath(socket)
	for i := range c.Clusters {
		cl := &c.Clusters[i]
		if cl.Server != "" {
			continue
		}
		have := cl.Socket
		if have == "" {
			have = ResolveSocket()
		}
		if cleanPath(have) == want {
			return cl
		}
	}
	return nil
}

func cleanPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	return filepath.Clean(p)
}

// configPath returns ~/.marvel/config.yaml.
func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".marvel", "config.yaml"), nil
}

// Load reads the config from ~/.marvel/config.yaml.
// Returns a default config if the file doesn't exist.
func Load() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultConfig(), nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	// A bad cluster name is reported, not repaired, and the parsed config
	// still comes back beside the error: a caller that can proceed per
	// cluster (listing, removing the offender, resolving an unaffected
	// cluster) checks errors.Is(err, ErrInvalidClusterName) and carries on;
	// every other caller treats it as the failure it is.
	if err := cfg.Validate(); err != nil {
		return &cfg, err
	}
	return &cfg, nil
}

// ErrInvalidClusterName marks a cluster whose name is outside the subject
// token class. Callers match it with errors.Is.
var ErrInvalidClusterName = errors.New("invalid cluster name")

// ValidateClusterName checks a cluster name against the closed class
// [A-Za-z0-9_-]. The name is the subject token for the cluster's JetStream
// domain, its global inbox, and its presence keys (R-94), so a dot, star,
// right angle bracket, or space would build a subject that is not the
// cluster's. It rejects, naming the offending byte, and never rewrites
// (the R-76 validToken shape from the director shim).
func ValidateClusterName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: name is empty", ErrInvalidClusterName)
	}
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-'
		if !ok {
			return fmt.Errorf("%w: %q contains %q; the class is [A-Za-z0-9_-] and a name is rejected, not rewritten, because a dot, star or right angle bracket would build a subject that is not this cluster's (R-94)", ErrInvalidClusterName, name, string(r))
		}
	}
	return nil
}

// Validate checks every cluster name and bus section. It reports all
// offenders at once, joined, so one load names every entry the operator has
// to fix.
func (c *Config) Validate() error {
	var errs []error
	for _, cl := range c.Clusters {
		if err := ValidateClusterName(cl.Name); err != nil {
			errs = append(errs, err)
		}
		if err := ValidateServices(&cl); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Save writes the config to ~/.marvel/config.yaml.
func Save(cfg *Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	return os.WriteFile(path, data, 0o600)
}

// defaultConfig returns the config for a fresh install — just the local
// cluster.
//
// Socket is deliberately left empty rather than filled with the resolved
// default, so the address is computed at use time. Baking a path here
// would freeze one home's layout into a file that may be read under
// another, which is the same class of mistake as the hardcoded constant
// this replaced.
func defaultConfig() *Config {
	return &Config{
		Clusters: []Cluster{
			{Name: "local"},
		},
		CurrentCluster: "local",
	}
}

// ResolveCluster returns the connection address for a cluster name.
// If name is empty, uses current_cluster.
func (c *Config) ResolveCluster(name string) (string, error) {
	cl, err := c.GetCluster(name)
	if err != nil {
		return "", err
	}
	if cl == nil {
		return ResolveSocket(), nil
	}
	if cl.Server != "" {
		return cl.Server, nil
	}
	if cl.Socket != "" {
		return cl.Socket, nil
	}
	return ResolveSocket(), nil
}

// GetCluster returns the Cluster struct for a given name, or for the
// current cluster when name is empty. Returns (nil, nil) when there is
// no configured cluster at all (fresh install).
func (c *Config) GetCluster(name string) (*Cluster, error) {
	if name == "" {
		name = c.CurrentCluster
	}
	if name == "" {
		return nil, nil
	}
	for i := range c.Clusters {
		if c.Clusters[i].Name == name {
			return &c.Clusters[i], nil
		}
	}
	return nil, fmt.Errorf("unknown cluster %q (run 'marvel config list' to see available clusters)", name)
}

// AddCluster adds or updates a cluster in the config. identity is
// optional and is preserved or updated when provided. The name must be in
// the subject token class (ValidateClusterName); a bad name is refused
// before anything is written.
func (c *Config) AddCluster(name, addr, identity string) error {
	if err := ValidateClusterName(name); err != nil {
		return err
	}
	for i, cl := range c.Clusters {
		if cl.Name == name {
			if isMRVL(addr) || isSSH(addr) {
				c.Clusters[i].Server = addr
				c.Clusters[i].Socket = ""
			} else {
				c.Clusters[i].Socket = addr
				c.Clusters[i].Server = ""
			}
			if identity != "" {
				c.Clusters[i].Identity = identity
			}
			return nil
		}
	}

	cl := Cluster{Name: name, Identity: identity}
	if isMRVL(addr) || isSSH(addr) {
		cl.Server = addr
	} else {
		cl.Socket = addr
	}
	c.Clusters = append(c.Clusters, cl)
	return nil
}

// RemoveCluster removes a cluster from the config.
func (c *Config) RemoveCluster(name string) error {
	for i, cl := range c.Clusters {
		if cl.Name == name {
			c.Clusters = append(c.Clusters[:i], c.Clusters[i+1:]...)
			if c.CurrentCluster == name {
				c.CurrentCluster = ""
				if len(c.Clusters) > 0 {
					c.CurrentCluster = c.Clusters[0].Name
				}
			}
			return nil
		}
	}
	return fmt.Errorf("cluster %q not found", name)
}

func isMRVL(addr string) bool {
	return len(addr) >= 7 && addr[:7] == "mrvl://"
}

func isSSH(addr string) bool {
	return len(addr) >= 6 && addr[:6] == "ssh://"
}
