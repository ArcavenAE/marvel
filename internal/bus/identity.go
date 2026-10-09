package bus

import (
	"errors"
	"time"

	"github.com/arcavenae/marvel/internal/childproof"
)

// identitySchema is the version of the sidecar record.
const identitySchema = 1

// identityRecord is the sidecar next to the bus pidfile: what the kernel said
// about the broker when marvel started it (docs/design/bus-pidfile-identity.md
// section 3.1). The pidfile itself stays a bare pid, so an older binary reads
// it as before.
type identityRecord struct {
	Schema    int      `json:"schema"`
	PID       int      `json:"pid"`
	Start     string   `json:"start"`
	Exe       string   `json:"exe"`
	Argv      []string `json:"argv"`
	WrittenAt string   `json:"written_at"`
}

// sidecarPath is where the identity record for pidFile lives.
func sidecarPath(pidFile string) string { return pidFile + ".identity" }

var errIdentityUnbuilt = errors.New("bus: identity record not built")

// writeIdentity records id for pid at path, temp file then rename, mode 0600.
func writeIdentity(path string, pid int, id childproof.Identity, now time.Time) error {
	return errIdentityUnbuilt
}

// readIdentityFile reads the record at path. A missing file is fs.ErrNotExist.
func readIdentityFile(path string) (identityRecord, error) {
	return identityRecord{}, errIdentityUnbuilt
}

// identity is the part of the record a proof compares.
func (r identityRecord) identity() childproof.Identity { return childproof.Identity{} }
