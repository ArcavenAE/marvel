// Package paths centralizes marvel's on-disk layout and permission rules.
//
// Layout (all under ~/.marvel/):
//
//	config.yaml                 client cluster config         0600
//	authorized_keys             daemon: authorized clients    0600
//	ssh_host_ed25519_key        daemon: host private key      0600
//	ssh_host_ed25519_key.pub    daemon: host public key       0644
//	known_hosts                 client: trusted servers       0644
//	keys/                       client: private keys dir      0700
//	keys/<name>                 client: private key           0600
//	keys/<name>.pub             client: public key            0644
//
// Modes follow OpenSSH conventions. Private material is 0600 or 0700;
// public material is 0644. The root directory is 0700.
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Expected modes for each path kind.
const (
	ModeDir        os.FileMode = 0o700
	ModePrivate    os.FileMode = 0o600
	ModePublic     os.FileMode = 0o644
	ModeConfig     os.FileMode = 0o600
	ModeAuthorized os.FileMode = 0o600
	ModeKnownHosts os.FileMode = 0o644
)

// DefaultClientKeyName is the name used for the default client key.
const DefaultClientKeyName = "client_ed25519"

// Layout is a resolved view of marvel's paths rooted at a given home.
type Layout struct {
	// Home is the ~/.marvel directory.
	Home string
}

// Default returns the layout rooted at $HOME/.marvel.
func Default() (Layout, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Layout{}, fmt.Errorf("cannot determine home directory: %w", err)
	}
	return Layout{Home: filepath.Join(home, ".marvel")}, nil
}

// WithHome returns a layout rooted at the given path. Useful in tests.
func WithHome(home string) Layout {
	return Layout{Home: home}
}

// Config returns the path to config.yaml.
func (l Layout) Config() string { return filepath.Join(l.Home, "config.yaml") }

// AuthorizedKeys returns the path to the daemon's authorized_keys file.
func (l Layout) AuthorizedKeys() string { return filepath.Join(l.Home, "authorized_keys") }

// HostKey returns the path to the daemon's SSH host private key.
func (l Layout) HostKey() string { return filepath.Join(l.Home, "ssh_host_ed25519_key") }

// HostKeyPub returns the path to the daemon's SSH host public key.
func (l Layout) HostKeyPub() string { return l.HostKey() + ".pub" }

// KnownHosts returns the path to the client's known_hosts file.
func (l Layout) KnownHosts() string { return filepath.Join(l.Home, "known_hosts") }

// KeysDir returns the client keys directory.
func (l Layout) KeysDir() string { return filepath.Join(l.Home, "keys") }

// ClientKey returns the path to a client private key by name (no extension).
func (l Layout) ClientKey(name string) string { return filepath.Join(l.KeysDir(), name) }

// ClientKeyPub returns the path to the corresponding public key.
func (l Layout) ClientKeyPub(name string) string { return l.ClientKey(name) + ".pub" }

// DefaultClientKey returns the path to the default client private key.
func (l Layout) DefaultClientKey() string { return l.ClientKey(DefaultClientKeyName) }

// EnsureHome creates ~/.marvel/ if it does not exist, with mode 0700.
// If it exists, verifies (but does not repair) its mode.
func (l Layout) EnsureHome() error {
	return ensureDir(l.Home, ModeDir)
}

// EnsureKeysDir creates ~/.marvel/keys/ if it does not exist, with mode 0700.
func (l Layout) EnsureKeysDir() error {
	if err := l.EnsureHome(); err != nil {
		return err
	}
	return ensureDir(l.KeysDir(), ModeDir)
}

// ensureDir creates dir with the given mode if missing. If it exists,
// returns nil without repairing — callers use Audit/Repair for that.
func ensureDir(dir string, mode os.FileMode) error {
	info, err := os.Stat(dir)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s exists but is not a directory", dir)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, mode); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	// MkdirAll honours umask; force the mode we want.
	if err := os.Chmod(dir, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", dir, err)
	}
	return nil
}

// Kind labels the role of a path for permission auditing.
type Kind int

const (
	KindDir Kind = iota
	KindPrivate
	KindPublic
	KindAuthorized
	KindKnownHosts
	KindConfig
)

// ExpectedMode returns the canonical mode for a path kind.
func ExpectedMode(k Kind) os.FileMode {
	switch k {
	case KindDir:
		return ModeDir
	case KindPrivate:
		return ModePrivate
	case KindPublic:
		return ModePublic
	case KindAuthorized:
		return ModeAuthorized
	case KindKnownHosts:
		return ModeKnownHosts
	case KindConfig:
		return ModeConfig
	}
	return 0
}

// Issue describes a single permission or ownership problem.
type Issue struct {
	Path    string
	Kind    Kind
	Want    os.FileMode
	Got     os.FileMode
	Missing bool
	Reason  string
}

func (i Issue) Error() string {
	if i.Missing {
		return fmt.Sprintf("%s: missing", i.Path)
	}
	return fmt.Sprintf("%s: mode %o, want %o (%s)", i.Path, i.Got, i.Want, i.Reason)
}

// CheckMode checks whether path has acceptable mode for its kind.
// For private material (0600/0700), any group or other bits are an error.
// For public material (0644), group/other read is allowed.
func CheckMode(path string, kind Kind) (*Issue, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Issue{Path: path, Kind: kind, Want: ExpectedMode(kind), Missing: true, Reason: "missing"}, nil
		}
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}

	got := info.Mode().Perm()
	want := ExpectedMode(kind)

	// Strictness: for private kinds, reject any bits outside 0700 for dirs
	// or 0600 for files. For public kinds, require exactly 0644.
	switch kind {
	case KindDir:
		if got&0o077 != 0 {
			return &Issue{Path: path, Kind: kind, Want: want, Got: got, Reason: "group/other access on private directory"}, nil
		}
	case KindPrivate, KindConfig, KindAuthorized:
		if got&0o077 != 0 {
			return &Issue{Path: path, Kind: kind, Want: want, Got: got, Reason: "group/other access on private file"}, nil
		}
	case KindPublic, KindKnownHosts:
		// Allow 0644; warn only if writable by group/other.
		if got&0o022 != 0 {
			return &Issue{Path: path, Kind: kind, Want: want, Got: got, Reason: "group/other writable"}, nil
		}
	}
	return nil, nil
}

// Audit returns a list of permission issues across marvel's on-disk state.
// Paths that do not exist yet are not reported as missing (that is the
// caller's concern) — only paths that exist but have wrong modes.
func (l Layout) Audit() ([]Issue, error) {
	checks := []struct {
		path string
		kind Kind
	}{
		{l.Home, KindDir},
		{l.KeysDir(), KindDir},
		{l.Config(), KindConfig},
		{l.AuthorizedKeys(), KindAuthorized},
		{l.HostKey(), KindPrivate},
		{l.HostKeyPub(), KindPublic},
		{l.KnownHosts(), KindKnownHosts},
	}

	var issues []Issue
	for _, c := range checks {
		issue, err := CheckMode(c.path, c.kind)
		if err != nil {
			return nil, err
		}
		if issue != nil && !issue.Missing {
			issues = append(issues, *issue)
		}
	}

	// Walk keys/ to include any additional client key files.
	keysDir := l.KeysDir()
	if info, err := os.Stat(keysDir); err == nil && info.IsDir() {
		entries, err := os.ReadDir(keysDir)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", keysDir, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			p := filepath.Join(keysDir, e.Name())
			var kind Kind
			if filepath.Ext(e.Name()) == ".pub" {
				kind = KindPublic
			} else {
				kind = KindPrivate
			}
			issue, err := CheckMode(p, kind)
			if err != nil {
				return nil, err
			}
			if issue != nil && !issue.Missing {
				issues = append(issues, *issue)
			}
		}
	}
	return issues, nil
}

// Repair attempts to chmod each reported issue back to its expected mode.
// Returns any issues that could not be repaired.
func (l Layout) Repair(issues []Issue) []Issue {
	var remaining []Issue
	for _, i := range issues {
		if i.Missing {
			remaining = append(remaining, i)
			continue
		}
		if err := os.Chmod(i.Path, i.Want); err != nil {
			i.Reason = fmt.Sprintf("chmod failed: %v", err)
			remaining = append(remaining, i)
		}
	}
	return remaining
}

// VerifyPrivateKeyMode refuses to use a private key whose mode is too open.
// OpenSSH behavior: bail out if the key is group- or world-accessible.
func VerifyPrivateKeyMode(path string) error {
	issue, err := CheckMode(path, KindPrivate)
	if err != nil {
		return err
	}
	if issue == nil {
		return nil
	}
	if issue.Missing {
		return fmt.Errorf("%s: no such file", path)
	}
	return fmt.Errorf(
		"permissions %o for %q are too open; run: chmod 600 %s",
		issue.Got, path, path,
	)
}
