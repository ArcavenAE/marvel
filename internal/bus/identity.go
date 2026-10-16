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

// identitySchema is the version of the sidecar record this binary writes.
// Schema 2 added LeafSeedFP. A schema 1 record is still read: it proves the
// broker as before and says nothing about its leaf seed, so a reader treats
// the leaf state as unknown (aae-orc-4h9xp).
const identitySchema = 2

// oldestIdentitySchema is the oldest record this binary still reads.
const oldestIdentitySchema = 1

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
	// LeafSeedFP is the fingerprint (SeedFingerprint) of the leaf seed the
	// broker was spawned with, empty when it was spawned without one. It is
	// never the seed: a fingerprint is not reversible. It is written at spawn
	// and on a broker restart, never on adopt, and it exists so a successor
	// that starts with an empty store can tell that a leaf was lost.
	LeafSeedFP string `json:"leaf_seed_fp,omitempty"`
}

// leafStateKnown reports whether the record says anything about the broker's
// leaf seed. A schema 1 record predates the field.
func (r identityRecord) leafStateKnown() bool { return r.Schema >= 2 }

// sidecarPath is where the identity record for pidFile lives.
func sidecarPath(pidFile string) string { return pidFile + ".identity" }

// writeIdentity records id for pid at path: a temporary file in the same
// directory, then a rename over the target, mode 0600, so a reader sees the
// old record or the new one and never half of either. The process group is
// not recorded; it is read live (design section 4.3).
func writeIdentity(path string, pid int, id childproof.Identity, now time.Time) error {
	return writeIdentityLeaf(path, pid, id, now, "")
}

// writeIdentityLeaf is writeIdentity with the fingerprint of the leaf seed the
// broker was spawned with ("" for none).
func writeIdentityLeaf(path string, pid int, id childproof.Identity, now time.Time, leafSeedFP string) error {
	rec := identityRecord{
		Schema: identitySchema, PID: pid, Start: id.Start, Exe: id.Exe, Argv: id.Argv,
		WrittenAt: now.UTC().Format(time.RFC3339), LeafSeedFP: leafSeedFP,
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
	if rec.Schema < oldestIdentitySchema || rec.Schema > identitySchema {
		return identityRecord{}, fmt.Errorf("bus: identity record %s: schema %d, want %d to %d", path, rec.Schema, oldestIdentitySchema, identitySchema)
	}
	return rec, nil
}

// identity is the part of the record a proof compares.
func (r identityRecord) identity() childproof.Identity {
	return childproof.Identity{Start: r.Start, Exe: r.Exe, Argv: r.Argv}
}
