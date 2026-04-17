// Package config manages marvel's client configuration, modeled after
// kubeconfig. Stores cluster connection details in ~/.marvel/config.yaml.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	// DefaultMRVLPort is the default port for the mrvl:// protocol.
	// Mnemonic: MRVL on phone keypad (M=6, R=7, V=8, L=5).
	DefaultMRVLPort = "6785"

	// DefaultSocket is the default Unix socket path for local access.
	DefaultSocket = "/tmp/marvel.sock"
)

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

// defaultConfig returns the config for a fresh install — just the local socket.
func defaultConfig() *Config {
	return &Config{
		Clusters: []Cluster{
			{Name: "local", Socket: DefaultSocket},
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
		return DefaultSocket, nil
	}
	if cl.Server != "" {
		return cl.Server, nil
	}
	if cl.Socket != "" {
		return cl.Socket, nil
	}
	return DefaultSocket, nil
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
