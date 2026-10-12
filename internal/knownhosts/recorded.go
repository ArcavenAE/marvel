package knownhosts

import (
	"errors"

	"golang.org/x/crypto/ssh"

	"github.com/arcavenae/marvel/internal/paths"
)

// Recorded returns the host keys known_hosts holds for hostport, a "host:port"
// pair as the ssh client presents it to the host key callback. An address with
// no entry, or a known_hosts file that does not exist yet, gives an empty
// slice and no error.
func Recorded(layout paths.Layout, hostport string) ([]ssh.PublicKey, error) {
	return nil, errors.New("not implemented")
}
