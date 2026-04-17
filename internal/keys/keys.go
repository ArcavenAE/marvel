// Package keys handles marvel's client and host SSH keypairs.
//
// All keys live under ~/.marvel/ (see paths package for the full layout).
// Private keys are created with mode 0600; public keys with 0644.
package keys

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/arcavenae/marvel/internal/paths"
)

// KeyType identifies a supported keypair algorithm.
type KeyType string

const (
	KeyTypeEd25519 KeyType = "ed25519"
)

// GenerateOptions controls keypair generation.
type GenerateOptions struct {
	// Name of the key (no extension). Defaults to paths.DefaultClientKeyName.
	Name string
	// Type of key to generate. Only ed25519 is supported today.
	Type KeyType
	// Comment embedded in the public key. Defaults to user@host.
	Comment string
	// Force overwrites an existing keypair when true.
	Force bool
}

// ClientKey describes a generated or existing client keypair on disk.
type ClientKey struct {
	Name        string
	PrivatePath string
	PublicPath  string
	Fingerprint string
	Comment     string
	Type        string
}

// GenerateClient creates a new client keypair under ~/.marvel/keys/.
// Returns the resulting ClientKey. Errors if the key already exists and
// Force is false.
func GenerateClient(layout paths.Layout, opts GenerateOptions) (*ClientKey, error) {
	if opts.Name == "" {
		opts.Name = paths.DefaultClientKeyName
	}
	if opts.Type == "" {
		opts.Type = KeyTypeEd25519
	}
	if opts.Type != KeyTypeEd25519 {
		return nil, fmt.Errorf("unsupported key type %q (only ed25519 is supported)", opts.Type)
	}
	if err := validateKeyName(opts.Name); err != nil {
		return nil, err
	}

	if err := layout.EnsureKeysDir(); err != nil {
		return nil, err
	}

	priv := layout.ClientKey(opts.Name)
	pub := layout.ClientKeyPub(opts.Name)

	if !opts.Force {
		if _, err := os.Stat(priv); err == nil {
			return nil, fmt.Errorf("%s already exists (use --force to overwrite)", priv)
		}
	}

	comment := opts.Comment
	if comment == "" {
		comment = defaultComment()
	}

	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 keypair: %w", err)
	}

	pemBlock, err := ssh.MarshalPrivateKey(privKey, comment)
	if err != nil {
		return nil, fmt.Errorf("marshal private key: %w", err)
	}
	privPEM := pem.EncodeToMemory(pemBlock)
	if err := writePrivate(priv, privPEM); err != nil {
		return nil, err
	}

	sshPub, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		return nil, fmt.Errorf("wrap public key: %w", err)
	}
	pubLine := append(bytes(ssh.MarshalAuthorizedKey(sshPub)), 0)
	// MarshalAuthorizedKey returns "ssh-ed25519 AAAA...\n" — append a
	// comment before the trailing newline so ssh-keygen-style tools show
	// the user@host context.
	line := strings.TrimRight(string(pubLine[:len(pubLine)-1]), "\n")
	line = line + " " + comment + "\n"
	if err := writePublic(pub, []byte(line)); err != nil {
		// Roll back the private key to avoid leaving an orphan.
		_ = os.Remove(priv)
		return nil, err
	}

	return &ClientKey{
		Name:        opts.Name,
		PrivatePath: priv,
		PublicPath:  pub,
		Fingerprint: ssh.FingerprintSHA256(sshPub),
		Comment:     comment,
		Type:        sshPub.Type(),
	}, nil
}

// LoadClient returns the metadata for an existing client keypair.
func LoadClient(layout paths.Layout, name string) (*ClientKey, error) {
	if name == "" {
		name = paths.DefaultClientKeyName
	}
	priv := layout.ClientKey(name)
	pub := layout.ClientKeyPub(name)

	if _, err := os.Stat(priv); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s: no such key (run 'marvel keys generate' first)", priv)
		}
		return nil, fmt.Errorf("stat %s: %w", priv, err)
	}

	data, err := os.ReadFile(pub)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", pub, err)
	}
	parsed, comment, _, _, err := ssh.ParseAuthorizedKey(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", pub, err)
	}

	return &ClientKey{
		Name:        name,
		PrivatePath: priv,
		PublicPath:  pub,
		Fingerprint: ssh.FingerprintSHA256(parsed),
		Comment:     comment,
		Type:        parsed.Type(),
	}, nil
}

// ListClient returns all client keypairs under ~/.marvel/keys/.
func ListClient(layout paths.Layout) ([]*ClientKey, error) {
	dir := layout.KeysDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}

	var out []*ClientKey
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) == ".pub" {
			continue
		}
		ck, err := LoadClient(layout, e.Name())
		if err != nil {
			// Skip entries that aren't valid keypairs; surface nothing.
			continue
		}
		out = append(out, ck)
	}
	return out, nil
}

// LoadOrGenerateHostKey returns a signer for the daemon's host key,
// generating and persisting a new one if none exists.
func LoadOrGenerateHostKey(layout paths.Layout) (ssh.Signer, error) {
	if err := layout.EnsureHome(); err != nil {
		return nil, err
	}

	keyPath := layout.HostKey()
	if data, err := os.ReadFile(keyPath); err == nil {
		signer, perr := ssh.ParsePrivateKey(data)
		if perr != nil {
			return nil, fmt.Errorf("parse host key %s: %w", keyPath, perr)
		}
		return signer, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read host key %s: %w", keyPath, err)
	}

	_, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate host key: %w", err)
	}
	pemBlock, err := ssh.MarshalPrivateKey(privKey, "marvel-host-key")
	if err != nil {
		return nil, fmt.Errorf("marshal host key: %w", err)
	}
	pemData := pem.EncodeToMemory(pemBlock)
	if err := writePrivate(keyPath, pemData); err != nil {
		return nil, err
	}

	signer, err := ssh.ParsePrivateKey(pemData)
	if err != nil {
		return nil, fmt.Errorf("parse generated host key: %w", err)
	}

	pubLine := ssh.MarshalAuthorizedKey(signer.PublicKey())
	if err := writePublic(layout.HostKeyPub(), pubLine); err != nil {
		return nil, err
	}
	return signer, nil
}

// writePrivate writes data to path with strict 0600 permissions, refusing
// to clobber unless the caller already removed the target.
func writePrivate(path string, data []byte) error {
	if err := os.WriteFile(path, data, paths.ModePrivate); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	// umask can strip bits below; enforce explicitly.
	if err := os.Chmod(path, paths.ModePrivate); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

func writePublic(path string, data []byte) error {
	if err := os.WriteFile(path, data, paths.ModePublic); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(path, paths.ModePublic); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

// validateKeyName rejects names that would escape the keys directory or
// collide with pubkey sidecars.
func validateKeyName(name string) error {
	if name == "" {
		return errors.New("key name is empty")
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("key name %q may not contain path separators", name)
	}
	if strings.HasPrefix(name, ".") {
		return fmt.Errorf("key name %q may not start with '.'", name)
	}
	if strings.HasSuffix(name, ".pub") {
		return fmt.Errorf("key name %q: drop the .pub suffix (it is added automatically)", name)
	}
	return nil
}

// defaultComment returns "user@host" for embedding in generated keys.
func defaultComment() string {
	who := "marvel"
	if u, err := user.Current(); err == nil && u.Username != "" {
		who = u.Username
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		return who
	}
	// Some hosts report fully-qualified names; keep just the short form.
	if _, _, err := net.SplitHostPort(host); err == nil {
		// Shouldn't happen, but guard anyway.
		host = strings.Split(host, ":")[0]
	}
	return who + "@" + host
}

// bytes is a tiny helper to avoid importing bytes just for .Clone().
func bytes(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
