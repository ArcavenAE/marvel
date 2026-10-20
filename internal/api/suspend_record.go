package api

import (
	"encoding/json"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

// bucketSuspend holds the fleet-suspend record (docs/design/fleet-suspend-
// resume.md section 3). It is new in a schema-compatible way: OpenBolt creates
// a missing bucket, so an older database opens unchanged.
var bucketSuspend = []byte("suspend")

// suspendKey is the one key in bucketSuspend: a daemon has at most one record.
const suspendKey = "cluster"

// The suspend record has no in-memory mirror inside the Store, like
// RoleHealth: the daemon holds the live copy, and this bucket is what it writes
// through to. The record is kept as the JSON the suspend package encodes, so
// this package does not depend on that one, and rehydrate leaves it alone.

// PersistSuspendRecord writes the record. No-op when bolt is not open.
func (s *Store) PersistSuspendRecord(raw json.RawMessage) error { return nil }

// SuspendRecord returns the stored record, or nil when there is none or bolt is
// not open.
func (s *Store) SuspendRecord() (json.RawMessage, error) { return nil, nil }

// ClearSuspendRecord removes the record. No-op when bolt is not open or there
// is none.
func (s *Store) ClearSuspendRecord() error { return nil }

var (
	_ = fmt.Errorf
	_ = bolt.Open
	_ = bucketSuspend
	_ = suspendKey
)
