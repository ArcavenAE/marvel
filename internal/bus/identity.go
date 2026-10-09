package bus

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

// writeIdentity records id for pid at path: a temporary file in the same
// directory, then a rename over the target, mode 0600, so a reader sees the
// old record or the new one and never half of either. The process group is
// not recorded; it is read live (design section 4.3).
func writeIdentity(path string, pid int, id childproof.Identity, now time.Time) error {
	rec := identityRecord{
		Schema: identitySchema, PID: pid, Start: id.Start, Exe: id.Exe, Argv: id.Argv,
		WrittenAt: now.UTC().Format(time.RFC3339),
	}
	body, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("bus: encode identity record: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("bus: write identity record: %w", err)
	}
	tmpName := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("bus: write identity record %s: %w", path, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(append(body, '\n')); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("bus: write identity record %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("bus: write identity record %s: %w", path, err)
	}
	return nil
}

// readIdentityFile reads the record at path. A missing file is fs.ErrNotExist,
// so a legacy pidfile (no record) is told from a corrupt record.
func readIdentityFile(path string) (identityRecord, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return identityRecord{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	var rec identityRecord
	if err := dec.Decode(&rec); err != nil {
		return identityRecord{}, fmt.Errorf("bus: identity record %s: %w", path, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return identityRecord{}, fmt.Errorf("bus: identity record %s: text after the record", path)
	}
	if rec.Schema != identitySchema {
		return identityRecord{}, fmt.Errorf("bus: identity record %s: schema %d, want %d", path, rec.Schema, identitySchema)
	}
	return rec, nil
}

// identity is the part of the record a proof compares.
func (r identityRecord) identity() childproof.Identity {
	return childproof.Identity{Start: r.Start, Exe: r.Exe, Argv: r.Argv}
}
