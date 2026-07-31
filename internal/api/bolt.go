package api

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

// L2 (durable record) for marvel's authoritative state — bbolt-backed
// write-ahead log behind the in-memory Store. Opens optionally via
// OpenBolt; default NewStore() stays in-memory only so existing tests
// don't pay the persistence cost.
//
// Persisted: Workspaces, Teams (incl. Roles + ShiftState), Sessions
// (incl. PaneID, State, Generation, Runtime, restart counters, CreatedAt),
// Endpoints, RoleHealth. Volatile session fields (LastHeartbeat,
// ContextPercent, HealthState, LastHealthCheck, PID) are persisted with
// the rest of the struct but treated as may-be-stale on rehydrate; the
// reconciler refreshes them.
//
// RoleHealth is the one bucket with no in-memory mirror here: its live
// copy is team.Controller's roleHealth map, and the Store is only the
// write-through record. See RoleHealthRecord.
//
// See orc question-marvel-transaction-log + finding-048 for the
// architectural reasoning.

var (
	bucketWorkspaces = []byte("workspaces")
	bucketTeams      = []byte("teams")
	bucketSessions   = []byte("sessions")
	bucketEndpoints  = []byte("endpoints")
	bucketRoleHealth = []byte("role_health")
	bucketMeta       = []byte("meta")
)

var (
	metaKeyResourceVersion = []byte("resource_version")
	metaKeySchemaVersion   = []byte("schema_version")
)

// boltSchemaVersion is the on-disk schema version. Bumped when the
// bucket layout or record format changes incompatibly. Rehydrate
// refuses to load a database with a higher version than the binary
// knows about; a lower version triggers a migration path (not yet
// implemented — failing fast is the right behavior for the spike).
const boltSchemaVersion uint64 = 1

// allBuckets is the canonical set of buckets bolt initializes on Open.
// Listed here so test helpers can iterate them.
var allBuckets = [][]byte{
	bucketWorkspaces,
	bucketTeams,
	bucketSessions,
	bucketEndpoints,
	bucketRoleHealth,
	bucketMeta,
}

// OpenBolt enables persistence on the Store. The bbolt DB at `path` is
// opened (created if absent), buckets are initialized, the schema
// version is checked, and any existing records are rehydrated into
// in-memory state.
//
// Must be called before the Store is used for writes if persistence
// is desired. Calling OpenBolt on a Store that has already accepted
// in-memory writes is undefined — the rehydrate would clobber the
// in-memory state. The intended call site is daemon startup, before
// the reconciler or any RPC handler has had a chance to mutate the
// store.
//
// Concurrent OpenBolt calls are not safe. Single-host, single-daemon
// assumption.
func (s *Store) OpenBolt(path string) error {
	if s.bolt != nil {
		return fmt.Errorf("bolt already open at %s", s.boltPath)
	}
	if err := ensureParentDir(path); err != nil {
		return err
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return fmt.Errorf("open bbolt at %s: %w", path, err)
	}

	// Initialize buckets + write schema version on first open. Wrapped
	// in a single Update so partial initialization can't leave the file
	// in a half-initialized state.
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, name := range allBuckets {
			if _, berr := tx.CreateBucketIfNotExists(name); berr != nil {
				return fmt.Errorf("create bucket %s: %w", string(name), berr)
			}
		}
		meta := tx.Bucket(bucketMeta)
		if v := meta.Get(metaKeySchemaVersion); v == nil {
			if perr := meta.Put(metaKeySchemaVersion, encodeUint64(boltSchemaVersion)); perr != nil {
				return fmt.Errorf("write schema version: %w", perr)
			}
		} else {
			got := decodeUint64(v)
			if got > boltSchemaVersion {
				return fmt.Errorf("on-disk schema version %d is newer than binary's %d — refusing to load",
					got, boltSchemaVersion)
			}
			// got < boltSchemaVersion would trigger migration; not
			// implemented yet, fail fast.
			if got < boltSchemaVersion {
				return fmt.Errorf("on-disk schema version %d is older than binary's %d — migration not implemented",
					got, boltSchemaVersion)
			}
		}
		return nil
	}); err != nil {
		_ = db.Close()
		return err
	}

	s.bolt = db
	s.boltPath = path

	if err := s.rehydrate(); err != nil {
		_ = db.Close()
		s.bolt = nil
		s.boltPath = ""
		return fmt.Errorf("rehydrate from %s: %w", path, err)
	}

	return nil
}

// CloseBolt flushes pending writes and closes the bbolt DB. Safe to
// call when bolt was never opened (returns nil).
func (s *Store) CloseBolt() error {
	if s.bolt == nil {
		return nil
	}
	db := s.bolt
	s.bolt = nil
	s.boltPath = ""
	return db.Close()
}

// Checkpoint forces an fsync of pending writes without closing the DB.
// Called from daemon shutdown (both the detach and teardown paths, just
// before CloseBolt) and from Daemon.Checkpoint, the seam a future
// syscall.Exec self-update uses to flush before handing the process
// over. Safe to call when bolt was never opened.
func (s *Store) Checkpoint() error {
	if s.bolt == nil {
		return nil
	}
	return s.bolt.Sync()
}

// BoltPath returns the path of the open bbolt file, or empty string if
// bolt is not open. Used by tests and observability.
func (s *Store) BoltPath() string {
	return s.boltPath
}

// rehydrate loads every persisted record from bbolt into the in-memory
// state. The caller must hold the write lock — but we don't take it
// here because OpenBolt is documented to be called pre-use, where no
// concurrent readers exist. Lock acquisition is deferred to the
// reflexive invariant: rehydrate is called from OpenBolt only.
//
// Records are deserialized via cloneSession / cloneTeam helpers to
// match the path used by every other Store method, keeping snapshot
// semantics consistent.
func (s *Store) rehydrate() error {
	return s.bolt.View(func(tx *bolt.Tx) error {
		// Workspaces
		if err := tx.Bucket(bucketWorkspaces).ForEach(func(_, v []byte) error {
			var w Workspace
			if err := json.Unmarshal(v, &w); err != nil {
				return fmt.Errorf("unmarshal workspace: %w", err)
			}
			s.workspaces[w.Key()] = &w
			return nil
		}); err != nil {
			return err
		}
		// Teams
		if err := tx.Bucket(bucketTeams).ForEach(func(_, v []byte) error {
			var t Team
			if err := json.Unmarshal(v, &t); err != nil {
				return fmt.Errorf("unmarshal team: %w", err)
			}
			s.teams[t.Key()] = &t
			return nil
		}); err != nil {
			return err
		}
		// Sessions
		if err := tx.Bucket(bucketSessions).ForEach(func(_, v []byte) error {
			var sess Session
			if err := json.Unmarshal(v, &sess); err != nil {
				return fmt.Errorf("unmarshal session: %w", err)
			}
			s.sessions[sess.Key()] = &sess
			return nil
		}); err != nil {
			return err
		}
		// Endpoints
		if err := tx.Bucket(bucketEndpoints).ForEach(func(_, v []byte) error {
			var e Endpoint
			if err := json.Unmarshal(v, &e); err != nil {
				return fmt.Errorf("unmarshal endpoint: %w", err)
			}
			s.endpoints[e.Key()] = &e
			return nil
		}); err != nil {
			return err
		}
		return nil
	})
}

// persistPut writes a single record to bbolt and bumps the resource-
// version counter atomically. Called from mutation paths under the
// Store's write lock. Returns nil if bolt isn't open (in-memory-only
// mode is the default).
//
// The resource-version bump is per-transaction, so a Create+Update
// pair appears as two version increments — useful for future watch-
// resumption work. Single-host; concurrent transactions on the same
// bucket serialize on bbolt's internal lock.
func (s *Store) persistPut(bucket []byte, key string, value any) error {
	if s.bolt == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal %s/%s: %w", bucket, key, err)
	}
	return s.bolt.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket(bucket).Put([]byte(key), data); err != nil {
			return fmt.Errorf("put %s/%s: %w", bucket, key, err)
		}
		return bumpVersion(tx)
	})
}

// persistDelete removes a single record from bbolt and bumps the
// resource-version counter atomically. Returns nil if bolt isn't open.
func (s *Store) persistDelete(bucket []byte, key string) error {
	if s.bolt == nil {
		return nil
	}
	return s.bolt.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket(bucket).Delete([]byte(key)); err != nil {
			return fmt.Errorf("delete %s/%s: %w", bucket, key, err)
		}
		return bumpVersion(tx)
	})
}

// RoleHealthRecord is the durable form of a role's crash-loop state:
// restart count, last restart, and the deadline before which the
// reconciler refuses to spawn a replacement. Key is workspace/team/role,
// matching team.Controller's map key.
//
// Unlike every other persisted type, RoleHealth has no in-memory mirror
// inside the Store. The controller's map is the live copy and this
// bucket is what it writes through to. So the three methods below read
// and write bbolt directly instead of a Store map, and rehydrate()
// leaves this bucket alone: the controller pulls it in via
// RehydrateRoleHealth at daemon start.
//
// Without this, a role frozen at MaxRestarts saturation came back from
// a daemon restart with an empty counter and got a free respawn: the
// gap bolt.go's package doc used to list as the L2 leftover.
type RoleHealthRecord struct {
	Key           string    `json:"key"`
	RestartCount  int       `json:"restart_count"`
	LastRestartAt time.Time `json:"last_restart_at"`
	BackoffUntil  time.Time `json:"backoff_until"`
}

// PersistRoleHealth writes one role's crash-loop state. No-op when bolt
// is not open.
func (s *Store) PersistRoleHealth(rec RoleHealthRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persistPut(bucketRoleHealth, rec.Key, rec)
}

// ListRoleHealth returns every persisted role-health record. Returns nil
// when bolt is not open, so a caller in in-memory-only mode starts with
// an empty map, the pre-L2 behavior.
func (s *Store) ListRoleHealth() ([]RoleHealthRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.bolt == nil {
		return nil, nil
	}
	var out []RoleHealthRecord
	if err := s.bolt.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketRoleHealth).ForEach(func(k, v []byte) error {
			var rec RoleHealthRecord
			if err := json.Unmarshal(v, &rec); err != nil {
				return fmt.Errorf("unmarshal role health %s: %w", string(k), err)
			}
			if rec.Key == "" {
				rec.Key = string(k)
			}
			out = append(out, rec)
			return nil
		})
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteRoleHealth removes one role's crash-loop state. The controller's
// cascade-clear paths call this so a re-applied manifest doesn't inherit
// a frozen backoff window from a prior generation of the same role name.
func (s *Store) DeleteRoleHealth(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persistDelete(bucketRoleHealth, key)
}

// ResourceVersion returns the current monotonic version counter — the
// resourceVersion analog from kubernetes. Bumped on every persisted
// mutation. Returns 0 if bolt is not open.
func (s *Store) ResourceVersion() uint64 {
	if s.bolt == nil {
		return 0
	}
	var v uint64
	_ = s.bolt.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bucketMeta).Get(metaKeyResourceVersion)
		v = decodeUint64(raw)
		return nil
	})
	return v
}

// bumpVersion increments the monotonic resource-version counter inside
// the given bbolt transaction. Idempotent within a tx (writes once per
// commit). Single-host single-writer assumption — no need for compare-
// and-swap.
func bumpVersion(tx *bolt.Tx) error {
	meta := tx.Bucket(bucketMeta)
	cur := decodeUint64(meta.Get(metaKeyResourceVersion))
	return meta.Put(metaKeyResourceVersion, encodeUint64(cur+1))
}

// encodeUint64 / decodeUint64 are the on-disk representation for the
// version counter (and any future numeric meta keys). Big-endian so
// lexicographic byte order matches numeric order — useful if we ever
// want to range-scan version keys.
func encodeUint64(v uint64) []byte {
	out := make([]byte, 8)
	binary.BigEndian.PutUint64(out, v)
	return out
}

func decodeUint64(b []byte) uint64 {
	if len(b) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(b)
}

// ensureParentDir creates the parent directory of `path` if it doesn't
// exist. bbolt requires the parent to exist before Open.
func ensureParentDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return nil
}
