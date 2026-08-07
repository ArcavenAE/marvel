// Package config manages marvel's client configuration, modeled after
// kubeconfig. Stores cluster connection details in ~/.marvel/config.yaml.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/arcavenae/marvel/internal/paths"
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
	return &cfg, nil
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
// optional and is preserved or updated when provided.
func (c *Config) AddCluster(name, addr, identity string) {
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
			return
		}
	}

	cl := Cluster{Name: name, Identity: identity}
	if isMRVL(addr) || isSSH(addr) {
		cl.Server = addr
	} else {
		cl.Socket = addr
	}
	c.Clusters = append(c.Clusters, cl)
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
