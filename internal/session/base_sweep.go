package session

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// The states of a temp base in an empty-base listing.
const (
	// BaseEmpty is a base that holds nothing: a confirmed sweep removes it.
	BaseEmpty = "empty"
	// BaseKept is a base the sweep leaves, with the reason in the listing.
	BaseKept = "kept"
)

// EmptyBaseEntry is one other layout's temp base and what the sweep does or
// did with it.
type EmptyBaseEntry struct {
	Path    string `json:"path"`
	State   string `json:"state"`
	Why     string `json:"why,omitempty"`
	Removed bool   `json:"removed,omitempty"`
}

// EmptyBaseSweep is the result of an empty-base sweep, a dry run unless
// Confirmed.
type EmptyBaseSweep struct {
	Confirmed bool             `json:"confirmed"`
	Root      string           `json:"root"`
	Bases     []EmptyBaseEntry `json:"bases"`
	Empty     int              `json:"empty"`
	Removed   int              `json:"removed"`
}

// baseSweeper holds what one empty-base sweep reads and the two seams a test
// uses to reach cases a scratch directory cannot make.
type baseSweeper struct {
	root string
	// ownBase is this layout's own temp base name, which is never listed: a
	// running daemon keeps its base.
	ownBase string
	// owned reports whether a file belongs to this user; ownedByMe in production.
	owned func(os.FileInfo) bool
	// beforeRemove, when set, runs after the checks and before the rmdir, so a
	// test can fill a base in that window.
	beforeRemove func(path string)
}

// SweepEmptyBases lists, and with confirm removes, the temp bases of other
// layouts that hold nothing. A base is removed only when it is a plain
// directory named marvel-h- and eight lowercase hex digits, mode 0700, owned
// by this user, and empty; the removal is one rmdir, never a recursive delete,
// so a base that gains an entry between the check and the removal is kept by
// the kernel. This layout's own base is never listed. Without confirm it
// changes nothing.
func (m *Manager) SweepEmptyBases(confirm bool) EmptyBaseSweep {
	s := m.homeSweeper()
	return baseSweeper{root: s.tmpDir, ownBase: s.ownTmpBase, owned: ownedByMe}.sweep(confirm)
}

func (b baseSweeper) sweep(confirm bool) EmptyBaseSweep {
	out := EmptyBaseSweep{Confirmed: confirm, Root: b.root}
	if b.root == "" {
		return out
	}
	entries, err := os.ReadDir(b.root)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !otherBaseRE.MatchString(e.Name()) || e.Name() == b.ownBase {
			continue
		}
		entry := b.sweepBase(filepath.Join(b.root, e.Name()), confirm)
		if entry.State == BaseEmpty {
			out.Empty++
		}
		if entry.Removed {
			out.Removed++
		}
		out.Bases = append(out.Bases, entry)
	}
	return out
}

func (b baseSweeper) sweepBase(path string, confirm bool) EmptyBaseEntry {
	e := EmptyBaseEntry{Path: path, State: BaseKept}
	fi, err := os.Lstat(path)
	switch {
	case err != nil:
		e.Why = err.Error()
		return e
	case !fi.IsDir():
		e.Why = "not a plain directory"
		return e
	case fi.Mode().Perm() != 0o700:
		e.Why = fmt.Sprintf("mode %v, want 0700", fi.Mode().Perm())
		return e
	case !b.owned(fi):
		e.Why = "not owned by this user"
		return e
	}
	inner, err := os.ReadDir(path)
	switch {
	case err != nil:
		e.Why = fmt.Sprintf("cannot be read: %v", err)
		return e
	case len(inner) > 0:
		e.Why = fmt.Sprintf("holds %d entr%s", len(inner), plural(len(inner)))
		return e
	}
	e.State = BaseEmpty
	if !confirm {
		return e
	}
	if b.beforeRemove != nil {
		b.beforeRemove(path)
	}
	// rmdir alone: it fails on a directory that is not empty and on a link, so
	// nothing below the base and nothing the base points to can be removed.
	if err := syscall.Rmdir(path); err != nil {
		e.State, e.Why = BaseKept, fmt.Sprintf("not removed: %v", err)
		return e
	}
	e.Removed = true
	return e
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
