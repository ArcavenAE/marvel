package register

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// PullFileName is the name of the register's pull file inside its directory.
const PullFileName = "register.json"

// Pull is the pull file: the rows the daemon computed, and the time of the pass
// that wrote them. WrittenAt is the one key outside the rows' own keys. It is
// the pass time and not any cell's observed_at, because cells expire on their
// own clocks.
type Pull struct {
	WrittenAt time.Time `json:"written_at"`
	Rows      []Row     `json:"rows"`
}

// Read is what ReadFile returns. A file inside its age carries the rows. A file
// older than the limit carries NotRun and no rows, so a reader cannot take the
// rows of a stale pass for a current one.
type Read struct {
	WrittenAt time.Time
	Rows      []Row
	// NotRun is "not-run since <written_at>" for a stale file, and empty
	// otherwise.
	NotRun string
}

// ErrNoMaxAge is returned by ReadFile for a limit of zero or less. The limit
// has no default: a reader says how old a pass it will accept.
var ErrNoMaxAge = errors.New("register pull file: max age must be positive")

// WriteFile writes the pull file name in dir for the pass at at. The file is
// built in a temp file in dir, created 0600, and renamed over name, so a reader
// sees the previous file or the new one and never part of either. dir must be a
// directory the daemon's user owns. On any failure the temp file is removed.
func WriteFile(dir, name string, rows []Row, at time.Time) error {
	return writeFile(dir, name, rows, at, nil)
}

// writeFile is WriteFile with a hook that runs once the temp file exists and
// before any data is written to it, so a test can read the destination at the
// point an in-place write would have already damaged it.
func writeFile(dir, name string, rows []Row, at time.Time, hook func()) (err error) {
	if err := checkDir(dir); err != nil {
		return err
	}
	if name == "" || filepath.Base(name) != name {
		return fmt.Errorf("register pull file: name %q is not one path element", name)
	}
	if rows == nil {
		rows = []Row{}
	}
	data, err := json.Marshal(Pull{WrittenAt: at.UTC(), Rows: rows})
	if err != nil {
		return fmt.Errorf("register pull file: encode: %w", err)
	}
	f, err := os.CreateTemp(dir, "."+name+".tmp-*")
	if err != nil {
		return fmt.Errorf("register pull file: create temp: %w", err)
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	if hook != nil {
		hook()
	}
	if _, err = f.Write(data); err != nil {
		return fmt.Errorf("register pull file: write: %w", err)
	}
	if err = f.Sync(); err != nil {
		return fmt.Errorf("register pull file: sync: %w", err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("register pull file: close: %w", err)
	}
	if err = os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("register pull file: rename: %w", err)
	}
	return nil
}

// checkDir refuses a destination that is not a directory the daemon's user owns.
func checkDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("register pull file: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("register pull file: %s is not a directory", dir)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Getuid() {
		return fmt.Errorf("register pull file: %s is not owned by the daemon's user", dir)
	}
	return nil
}

// ReadFile reads the pull file at path as of now. A file written longer than
// maxAge ago reads as not-run since its pass time, with no rows. A missing or
// unreadable file is an error and never reads as stale.
func ReadFile(path string, maxAge time.Duration, now time.Time) (Read, error) {
	if maxAge <= 0 {
		return Read{}, ErrNoMaxAge
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Read{}, fmt.Errorf("register pull file: %w", err)
	}
	var p Pull
	if err := json.Unmarshal(data, &p); err != nil {
		return Read{}, fmt.Errorf("register pull file: decode: %w", err)
	}
	if p.WrittenAt.IsZero() {
		return Read{}, errors.New("register pull file: no written_at")
	}
	if now.Sub(p.WrittenAt) > maxAge {
		return Read{
			WrittenAt: p.WrittenAt,
			NotRun:    "not-run since " + p.WrittenAt.UTC().Format(time.RFC3339),
		}, nil
	}
	return Read{WrittenAt: p.WrittenAt, Rows: p.Rows}, nil
}
