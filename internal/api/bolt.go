package api

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
// Endpoints, RoleHealth, ScheduleStatus. Volatile session fields (LastHeartbeat,
// HealthState, LastHealthCheck, PID) are persisted with the rest of the
// struct but treated as may-be-stale on rehydrate; the reconciler
// refreshes them.
//
// A STREAM-DERIVED SessionContext block is the exception: rehydrate zeroes
// it, because nothing refreshes it after a restart. The FIFO reader is gone
// and an adopted pane has no instance entry, so marvel will never produce
// another reading for a session it inherits. An absent CTX% after a restart
// is honest; a frozen percentage indistinguishable from a live one is not.
//
// A cooperative-heartbeat reading survives, as it did before the accountant
// existed: the agent that sent it keeps sending, so it is may-be-stale in
// the same way as the LastHeartbeat beside it, not orphaned. The two are
// told apart by ContextRequests, which only the accountant writes.
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
	bucketPolicies   = []byte("policies")
	bucketRoleHealth = []byte("role_health")
	// bucketScheduleStatus is new in a schema-compatible way: OpenBolt
	// creates a missing bucket, so an older database opens unchanged.
	bucketScheduleStatus = []byte("schedule_status")
	bucketMeta           = []byte("meta")
)

var (
	metaKeyResourceVersion = []byte("resource_version")
	metaKeySchemaVersion   = []byte("schema_version")
)

// boltSchemaVersion is the on-disk schema version. Bumped when the
// bucket layout or record format changes incompatibly, or when a record
// needs a one-time pass. OpenBolt refuses a database newer than the binary
// and migrates a version-1 database; version 2 stamps the teams that had no
// root so they keep their place (see migrateV1 and
// docs/design/seat-bootstrap.md section 3).
const boltSchemaVersion uint64 = 2

// allBuckets is the canonical set of buckets bolt initializes on Open.
// Listed here so test helpers can iterate them.
var allBuckets = [][]byte{
	bucketWorkspaces,
	bucketTeams,
	bucketSessions,
	bucketEndpoints,
	bucketPolicies,
	bucketRoleHealth,
	bucketScheduleStatus,
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
	return s.OpenBoltWithOptions(path, BoltOptions{})
}

// OpenBoltWithOptions is OpenBolt with the migration inputs.
func (s *Store) OpenBoltWithOptions(path string, opts BoltOptions) error {
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

	version, err := onDiskSchemaVersion(db)
	if err != nil {
		_ = db.Close()
		return err
	}
	var stamps []LegacyStamp
	switch {
	case version > boltSchemaVersion:
		_ = db.Close()
		return fmt.Errorf("on-disk schema version %d is newer than binary's %d — refusing to load",
			version, boltSchemaVersion)
	case version == 1:
		stamps, err = migrateV1(db, path, opts)
		if err != nil {
			_ = db.Close()
			return err
		}
	case version != 0 && version < boltSchemaVersion:
		_ = db.Close()
		return fmt.Errorf("on-disk schema version %d is older than binary's %d — no migration from it", version, boltSchemaVersion)
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
		if meta.Get(metaKeySchemaVersion) == nil {
			if perr := meta.Put(metaKeySchemaVersion, encodeUint64(boltSchemaVersion)); perr != nil {
				return fmt.Errorf("write schema version: %w", perr)
			}
		}
		return nil
	}); err != nil {
		_ = db.Close()
		return err
	}
	s.legacyStamps = stamps

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
			// Nothing can refresh a STREAM reading for a session this
			// daemon did not launch, so drop it rather than serve a frozen
			// percentage. A heartbeat reading is refreshed by the agent
			// itself and survives. See the persistence note at the top of
			// the file.
			//
			// Keyed on the declared producer, not on "has a request count":
			// an accountant reading that never resolved a window looks
			// identical to a heartbeat by field shape, so the old test
			// dropped some readings it should have kept and kept some it
			// should have dropped. See aae-orc-ibu9.
			//
			// The ContextRequests arm is the pre-ContextSource legacy
			// case: marvel upgrades in place (daemon reexec), so a bolt
			// file written by an older binary carries readings with no
			// declared producer. Only the accountant ever wrote a request
			// count, so it is a sound legacy marker. Remove once no
			// supported upgrade path crosses this boundary.
			if sess.ContextSource == ContextSourceAccountant ||
				(sess.ContextSource == ContextSourceNone && sess.ContextRequests > 0) {
				sess.SessionContext = SessionContext{}
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
		// Policies
		if err := tx.Bucket(bucketPolicies).ForEach(func(_, v []byte) error {
			var p Policy
			if err := json.Unmarshal(v, &p); err != nil {
				return fmt.Errorf("unmarshal policy: %w", err)
			}
			s.policies[p.Key()] = &p
			return nil
		}); err != nil {
			return err
		}
		// Schedule status
		return tx.Bucket(bucketScheduleStatus).ForEach(func(k, v []byte) error {
			var st ScheduleStatus
			if err := json.Unmarshal(v, &st); err != nil {
				return fmt.Errorf("unmarshal schedule status %s: %w", string(k), err)
			}
			st.Key = string(k)
			s.scheduleStatus[st.Key] = &st
			return nil
		})
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

// BoltOptions are the inputs the v1 to v2 migration needs that the file does
// not hold.
type BoltOptions struct {
	// LegacyCwd is the directory a team with no resolvable root is stamped
	// with. Empty means the process's working directory at the time of the
	// migration.
	LegacyCwd string
}

// LegacyStamp records one team stamped by the migration.
type LegacyStamp struct {
	TeamKey string
	Dir     string
}

// LegacyStamps returns the teams the migration stamped when this store was
// opened, so the daemon can announce them once the commit is durable.
func (s *Store) LegacyStamps() []LegacyStamp {
	return append([]LegacyStamp(nil), s.legacyStamps...)
}

// boltMigrationFault, when set by a test, is called at named stages of the
// migration ("backup", "stamp") and its error aborts that stage.
var boltMigrationFault func(stage string) error

func migrationFault(stage string) error {
	if boltMigrationFault == nil {
		return nil
	}
	return boltMigrationFault(stage)
}

// onDiskSchemaVersion reads the version a file carries: zero for a file with
// no meta bucket or no version, which is a fresh store.
func onDiskSchemaVersion(db *bolt.DB) (uint64, error) {
	var v uint64
	err := db.View(func(tx *bolt.Tx) error {
		meta := tx.Bucket(bucketMeta)
		if meta == nil {
			return nil
		}
		if raw := meta.Get(metaKeySchemaVersion); raw != nil {
			v = decodeUint64(raw)
		}
		return nil
	})
	return v, err
}

// migrateV1 upgrades a version-1 store in place. It writes `<store>.v1.bak`
// first, because the old binary refuses a newer store and nothing else keeps
// a way back; then, in one transaction, stamps every team that resolves no
// root with the directory the daemon started in and writes version 2. The
// transaction commits whole or not at all. The backup is never overwritten:
// an existing one means an earlier migration was interrupted or the file is
// stale, and the daemon refuses to guess which.
func migrateV1(db *bolt.DB, path string, opts BoltOptions) ([]LegacyStamp, error) {
	bak := path + ".v1.bak"
	if _, err := os.Lstat(bak); err == nil {
		return nil, fmt.Errorf("%s exists beside a v1 store: a previous migration was interrupted, or the file is a stale backup. Move it aside and restart", bak)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("check for backup %s: %w", bak, err)
	}
	if err := writeV1Backup(db, bak); err != nil {
		return nil, fmt.Errorf("back up the v1 store before migrating: %w", err)
	}

	cwd := opts.LegacyCwd
	if cwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("migrate v1 store: the daemon's working directory is unknown: %w", err)
		}
		cwd = wd
	}

	var stamps []LegacyStamp
	if err := db.Update(func(tx *bolt.Tx) error {
		stamps = stamps[:0]
		for _, name := range allBuckets {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return fmt.Errorf("create bucket %s: %w", string(name), err)
			}
		}
		roots := map[string]string{}
		if err := tx.Bucket(bucketWorkspaces).ForEach(func(_, v []byte) error {
			var w Workspace
			if err := json.Unmarshal(v, &w); err != nil {
				return fmt.Errorf("unmarshal workspace: %w", err)
			}
			roots[w.Name] = w.Root
			return nil
		}); err != nil {
			return err
		}
		teams := tx.Bucket(bucketTeams)
		type pending struct {
			key []byte
			val []byte
		}
		var writes []pending
		if err := teams.ForEach(func(k, v []byte) error {
			var t Team
			if err := json.Unmarshal(v, &t); err != nil {
				return fmt.Errorf("unmarshal team %s: %w", string(k), err)
			}
			if t.WorkDir != "" || roots[t.Workspace] != "" {
				return nil
			}
			t.WorkDir = cwd
			t.WorkDirSource = WorkDirSourceLegacy
			b, err := json.Marshal(t)
			if err != nil {
				return fmt.Errorf("marshal team %s: %w", string(k), err)
			}
			writes = append(writes, pending{key: append([]byte(nil), k...), val: b})
			stamps = append(stamps, LegacyStamp{TeamKey: string(k), Dir: cwd})
			return nil
		}); err != nil {
			return err
		}
		for _, w := range writes {
			if err := teams.Put(w.key, w.val); err != nil {
				return fmt.Errorf("stamp team %s: %w", string(w.key), err)
			}
		}
		if err := migrationFault("stamp"); err != nil {
			return err
		}
		return tx.Bucket(bucketMeta).Put(metaKeySchemaVersion, encodeUint64(2))
	}); err != nil {
		return nil, fmt.Errorf("migrate v1 store (backup kept at %s): %w", bak, err)
	}
	sort.Slice(stamps, func(i, j int) bool { return stamps[i].TeamKey < stamps[j].TeamKey })
	return stamps, nil
}

// WorkDirSourceLegacy marks a Team.WorkDir stamped by the v1 to v2 migration.
const WorkDirSourceLegacy = "legacy"

// writeV1Backup writes a consistent copy of the store to bak with mode 0600
// and fsyncs it. It goes through a temporary name and a rename, so a crash or
// a failed write never leaves a partial file under the name the next start
// would refuse on.
func writeV1Backup(db *bolt.DB, bak string) (err error) {
	if err := migrationFault("backup"); err != nil {
		return err
	}
	tmp := bak + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	if err = db.View(func(tx *bolt.Tx) error {
		_, werr := tx.WriteTo(f)
		return werr
	}); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, bak)
}

// keepLegacyStamp reports whether a team's legacy stamp survives a re-apply
// whose manifest declares the given workspace root and team workdir.
func keepLegacyStamp(live *Team, root, teamDir string) bool {
	return live.WorkDirSource == WorkDirSourceLegacy && root == "" && teamDir == ""
}

// LegacyPlacementNotes returns one note per team of this manifest that holds a
// legacy stamp and still names no root, for the apply output.
func (m *Manifest) LegacyPlacementNotes(store *Store) []string {
	var notes []string
	for _, mt := range m.Teams {
		live, err := store.GetTeam(m.Workspace.Name + "/" + mt.Name)
		if err != nil || !keepLegacyStamp(&live, m.Workspace.Root, mt.WorkDir) {
			continue
		}
		notes = append(notes, fmt.Sprintf("team %s: placement: legacy daemon cwd %s; declare a root", mt.Name, live.WorkDir))
	}
	return notes
}
