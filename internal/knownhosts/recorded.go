package knownhosts

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"

	"golang.org/x/crypto/ssh"
	xknownhosts "golang.org/x/crypto/ssh/knownhosts"

	"github.com/arcavenae/marvel/internal/paths"
)

// Recorded returns the host keys known_hosts holds for hostport, a "host:port"
// pair as the ssh client presents it to the host key callback. An address with
// no entry, or a known_hosts file that does not exist yet, gives an empty
// slice and no error. It reads the file and never writes it.
func Recorded(layout paths.Layout, hostport string) ([]ssh.PublicKey, error) {
	path := layout.KnownHosts()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	verify, err := xknownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}

	// The verifier reports the keys it holds for a host only as the Want of the
	// error it returns for a key that does not match. A key made here and thrown
	// away can match nothing, so the error is always that one.
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate probe key: %w", err)
	}
	probe, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("wrap probe key: %w", err)
	}
	err = verify(hostport, &net.TCPAddr{IP: net.IPv4zero}, probe)
	var keyErr *xknownhosts.KeyError
	if !errors.As(err, &keyErr) {
		return nil, fmt.Errorf("read keys for %s from %s: %w", hostport, path, err)
	}
	keys := make([]ssh.PublicKey, 0, len(keyErr.Want))
	for _, k := range keyErr.Want {
		keys = append(keys, k.Key)
	}
	return keys, nil
}
