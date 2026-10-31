package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"syscall"

	"github.com/arcavenae/marvel/internal/childproof"
	"github.com/arcavenae/marvel/internal/paths"
)

// The states of a harness home in a sweep listing.
const (
	// HomeLive is a home some session record names. It is never touched.
	HomeLive = "live"
	// HomeOrphan is a home no record names and that holds no codex history.
	// A confirmed sweep removes it.
	HomeOrphan = "orphan"
	// HomeKeptHistory is a home no record names that still holds codex
	// history, or whose history cannot be read. A confirmed sweep leaves it
	// in place and lists it, so an operator can decide.
	HomeKeptHistory = "kept-history"
)

// The actions on a daemon pid in a sweep listing.
const (
	DaemonSignal = "signal"
	DaemonLeft   = "left"
)

// homeNameRE is the name harnessHomeFor gives a home: the first twelve hex
// digits of a hash of the session key. Nothing else under a base is a home.
var homeNameRE = regexp.MustCompile(`^[0-9a-f]{12}$`)

// otherBaseRE is the name of another layout's temp base: marvel-h- and the
// four-byte layout tag (paths.Layout.Tag).
var otherBaseRE = regexp.MustCompile(`^marvel-h-[0-9a-f]{8}$`)

// daemonPidFiles are the pid files codex writes under a home for the
// app-server daemon it starts there, relative to the home.
var daemonPidFiles = []string{
	filepath.Join("app-server-daemon", "daemon.pid"),
	filepath.Join("app-server-daemon", "daemon-updater.pid"),
}

// maxPidFileBytes bounds a pid file read; a real one is a few hundred bytes.
const maxPidFileBytes = 4096

// daemonArg is the one whole argument a process must carry to count as the
// codex app-server daemon.
const daemonArg = "app-server"

// DaemonEntry is one daemon pid found in a home's own pid files.
type DaemonEntry struct {
	Pid       int    `json:"pid"`
	File      string `json:"file"`
	Action    string `json:"action"`
	Why       string `json:"why,omitempty"`
	Signalled bool   `json:"signalled,omitempty"`
	// Settled is true when the daemon is accounted for: it was signalled, is
	// gone, or the pid now belongs to another process. A home is removed only
	// when every pid file in it is settled.
	Settled bool `json:"settled,omitempty"`
}

// HomeEntry is one home under a base and what a sweep does or did with it.
type HomeEntry struct {
	Path    string        `json:"path"`
	State   string        `json:"state"`
	Why     string        `json:"why,omitempty"`
	Daemons []DaemonEntry `json:"daemons,omitempty"`
	Removed bool          `json:"removed,omitempty"`
}

// HomeSweep is the result of a sweep, a dry run unless Confirmed.
type HomeSweep struct {
	Confirmed bool        `json:"confirmed"`
	Bases     []string    `json:"bases"`
	Homes     []HomeEntry `json:"homes"`
	// Notes name a base the sweep skipped and why.
	Notes []string `json:"notes,omitempty"`
	// OtherBases and OtherHomes count other layouts' temp bases and the homes
	// inside them. They are counted read-only and never touched: another
	// layout's home may belong to a live seat this daemon has no record of.
	OtherBases int `json:"other_bases"`
	OtherHomes int `json:"other_homes"`
}

// homeSweeper holds what one sweep reads: the bases it may look in, the
// paths session records name, and the prober that proves a daemon pid.
type homeSweeper struct {
	bases    []string
	recorded []string
	// recordedNow, when set, is read again just before a home is removed, so a
	// record made since the listing keeps its home. It falls back to recorded.
	recordedNow func() []string
	prober      *childproof.Prober
	// owned reports whether a file belongs to this user. Nil means ownedByMe;
	// a test sets it to reach a directory owned by another user, which a test
	// cannot create without privilege.
	owned func(os.FileInfo) bool
	// tmpDir is where other layouts' temp bases are counted; empty skips it.
	tmpDir string
	// ownTmpBase is this layout's own temp base, left out of that count.
	ownTmpBase string
}

// SweepHomes lists, and with confirm reaps, the harness homes and orphaned
// codex daemons under this daemon's two bases: the durable base under the
// marvel home and the old per-layout temp base. A home a session record names
// is never touched. Without confirm it changes nothing.
func (m *Manager) SweepHomes(confirm bool) HomeSweep {
	return m.homeSweeper().sweep(confirm)
}

// homeSweeper builds the sweeper for this manager: its bases, and a view of
// the session records that is read again on every use.
func (m *Manager) homeSweeper() homeSweeper {
	s := homeSweeper{prober: childproof.New(), tmpDir: harnessTempRoot, owned: m.ownerCheck}
	s.bases = append(s.bases, m.HarnessHomeDir)
	if layout, err := paths.Default(); err == nil {
		s.bases = append(s.bases, layout.HarnessHomesDir())
		legacy := legacyHarnessHomeBase(layout.Tag())
		s.ownTmpBase = filepath.Base(legacy)
		s.bases = append(s.bases, legacy)
	}
	s.recordedNow = func() []string {
		var out []string
		for _, sess := range m.store.ListSessions() {
			if sess.HarnessHome != "" {
				out = append(out, sess.HarnessHome)
			}
		}
		return out
	}
	s.recorded = s.recordedNow()
	return s
}

// DeleteAndReap is Delete for a seat that is stopping for good, and then
// reaps that seat's harness home and the codex daemon under it by the same
// rules as the sweep: a home any session record names is untouched, a home
// with codex history is kept, a daemon pid is signalled only when proven. A
// key the reconciler has already given a new session reads as recorded, so its
// home stays. The reap runs only when the delete succeeded: a seat whose pane
// could not be killed keeps its row and its home.
//
// Restarts, shifts and respawns call Delete, not this: the same key reuses the
// same home.
func (m *Manager) DeleteAndReap(key string) error {
	sess, err := m.store.GetSession(key)
	if err != nil {
		return err
	}
	if err := m.Delete(key); err != nil {
		return err
	}
	m.reapSeatHome(key, sess.HarnessHome)
	return nil
}

// reapSeatHome reaps one home after its seat stopped. It acts only on a direct
// child of one of this daemon's bases whose name is twelve hex digits, the
// shape harnessHomeFor gives; any other recorded path is left and logged.
func (m *Manager) reapSeatHome(key, home string) {
	if home == "" {
		return
	}
	s := m.homeSweeper()
	home = filepath.Clean(home)
	if !homeNameRE.MatchString(filepath.Base(home)) {
		log.Printf("session %s: harness home %s is not a marvel home name, left in place", key, home)
		return
	}
	for _, base := range s.bases {
		base = filepath.Clean(base)
		if base == "." || filepath.Dir(home) != base {
			continue
		}
		if why, absent := baseRefusal(base, s.mine); why != "" {
			if !absent {
				log.Printf("session %s: harness home %s left in place: base %s: %s", key, home, base, why)
			}
			return
		}
		fi, err := os.Lstat(home)
		if err != nil || !fi.IsDir() || !s.mine(fi) {
			return
		}
		e := s.sweepHome(home, fi, statAll(s.recordedPaths()), true)
		log.Printf("session %s: harness home %s: %s, %s", key, home, e.State, reapSummary(e))
		return
	}
	log.Printf("session %s: harness home %s is outside this daemon's bases, left in place", key, home)
}

// reapSummary is one line of what a reap did to a home and its daemons.
func reapSummary(e HomeEntry) string {
	signalled := 0
	for _, d := range e.Daemons {
		if d.Signalled {
			signalled++
		}
	}
	switch {
	case e.Removed:
		return fmt.Sprintf("removed, %d daemon(s) signalled", signalled)
	default:
		return fmt.Sprintf("kept (%s), %d daemon(s) signalled", e.Why, signalled)
	}
}

// mine reports whether fi belongs to this user, through the sweeper's seam.
func (s homeSweeper) mine(fi os.FileInfo) bool {
	if s.owned != nil {
		return s.owned(fi)
	}
	return ownedByMe(fi)
}

// recordedPaths is the set of homes session records name, read now when the
// sweeper has a live source for it.
func (s homeSweeper) recordedPaths() []string {
	if s.recordedNow != nil {
		return s.recordedNow()
	}
	return s.recorded
}

func (s homeSweeper) sweep(confirm bool) HomeSweep {
	out := HomeSweep{Confirmed: confirm}
	recorded := statAll(s.recordedPaths())
	seen := map[string]bool{}
	for _, base := range s.bases {
		base = filepath.Clean(base)
		if base == "" || base == "." || seen[base] {
			continue
		}
		seen[base] = true
		if why, absent := baseRefusal(base, s.mine); why != "" {
			if !absent {
				out.Notes = append(out.Notes, fmt.Sprintf("base %s skipped: %s", base, why))
			}
			continue
		}
		out.Bases = append(out.Bases, base)
		entries, err := os.ReadDir(base)
		if err != nil {
			out.Notes = append(out.Notes, fmt.Sprintf("base %s skipped: %v", base, err))
			continue
		}
		for _, e := range entries {
			if !homeNameRE.MatchString(e.Name()) {
				continue
			}
			home := filepath.Join(base, e.Name())
			fi, err := os.Lstat(home)
			if err != nil || !fi.IsDir() || !s.mine(fi) {
				continue
			}
			out.Homes = append(out.Homes, s.sweepHome(home, fi, recorded, confirm))
		}
	}
	out.OtherBases, out.OtherHomes = s.countOtherLayouts()
	return out
}

// baseRefusal returns why a base is not one the sweep may look in: it must be
// a plain directory (not a link) that is private to this user. absent is true
// when the base is simply not there, which is not worth a note.
func baseRefusal(base string, mine func(os.FileInfo) bool) (why string, absent bool) {
	fi, err := os.Lstat(base)
	switch {
	case os.IsNotExist(err):
		return "does not exist", true
	case err != nil:
		return err.Error(), false
	case !fi.IsDir():
		return "not a plain directory", false
	case fi.Mode().Perm() != 0o700:
		return fmt.Sprintf("mode %v, want 0700", fi.Mode().Perm()), false
	case !mine(fi):
		return "not owned by this user", false
	}
	return "", false
}

// statAll stats each path, following links, and drops the ones that are gone.
func statAll(paths []string) []os.FileInfo {
	var out []os.FileInfo
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil {
			out = append(out, fi)
		}
	}
	return out
}

func isRecorded(fi os.FileInfo, recorded []os.FileInfo) bool {
	for _, r := range recorded {
		if os.SameFile(fi, r) {
			return true
		}
	}
	return false
}

func (s homeSweeper) sweepHome(home string, fi os.FileInfo, recorded []os.FileInfo, confirm bool) HomeEntry {
	e := HomeEntry{Path: home}
	if isRecorded(fi, recorded) {
		e.State = HomeLive
		e.Why = "a session record names it"
		return e
	}
	if why := historyReason(home); why != "" {
		e.State, e.Why = HomeKeptHistory, why
	} else {
		e.State, e.Why = HomeOrphan, "no session record names it and it holds no codex history"
	}
	e.Daemons = s.daemonsIn(home, confirm)
	if !confirm || e.State != HomeOrphan {
		return e
	}
	for _, d := range e.Daemons {
		if !d.Settled {
			e.Why += fmt.Sprintf("; kept because the daemon in %s is not accounted for", d.File)
			if d.Why != "" {
				e.Why += " (" + d.Why + ")"
			}
			return e
		}
	}
	// Re-read just before removing: a record may have named the home, or
	// history may have appeared, since the listing was built.
	now, err := os.Lstat(home)
	if err != nil || !now.IsDir() || isRecorded(now, statAll(s.recordedPaths())) || historyReason(home) != "" {
		e.Why += "; kept because it changed during the sweep"
		return e
	}
	if err := os.RemoveAll(home); err != nil {
		e.Why += fmt.Sprintf("; removal failed: %v", err)
		return e
	}
	e.Removed = true
	return e
}

// historyReason returns why a home counts as holding codex history, or ""
// when it holds none. Only a missing or empty sessions directory is none;
// anything unreadable or unusual is kept, so a doubt never deletes history.
func historyReason(home string) string {
	dir := filepath.Join(home, "sessions")
	fi, err := os.Lstat(dir)
	switch {
	case os.IsNotExist(err):
		return ""
	case err != nil:
		return fmt.Sprintf("its sessions directory cannot be read: %v", err)
	case !fi.IsDir():
		return "its sessions entry is not a plain directory"
	}
	entries, err := os.ReadDir(dir)
	switch {
	case err != nil:
		return fmt.Sprintf("its sessions directory cannot be read: %v", err)
	case len(entries) > 0:
		return fmt.Sprintf("holds codex history (%d entries under sessions)", len(entries))
	}
	return ""
}

// codexPidRecord is what codex writes in a daemon pid file: the pid and the
// kernel start time it read for that process.
type codexPidRecord struct {
	Pid             int64 `json:"pid"`
	ProcessIdentity struct {
		StartSeconds      int64 `json:"startSeconds"`
		StartMicroseconds int64 `json:"startMicroseconds"`
	} `json:"processIdentity"`
}

// daemonsIn reads the daemon pid files codex keeps inside home and, for each
// pid that is provably the recorded app-server process, signals it when
// confirm is set. Nothing is found by pattern: a pid is considered only when
// it is written in a pid file inside a home the sweep owns.
func (s homeSweeper) daemonsIn(home string, confirm bool) []DaemonEntry {
	var out []DaemonEntry
	for _, rel := range daemonPidFiles {
		file := filepath.Join(home, rel)
		rec, why, absent := readPidRecord(file)
		if absent {
			continue
		}
		d := DaemonEntry{File: rel}
		if why != "" {
			d.Action, d.Why = DaemonLeft, why
			out = append(out, d)
			continue
		}
		d.Pid = int(rec.Pid)
		start := recordedStart(rec.ProcessIdentity.StartSeconds, rec.ProcessIdentity.StartMicroseconds)
		if start == "" {
			d.Action, d.Why = DaemonLeft, "the recorded start time cannot be compared on this platform"
			out = append(out, d)
			continue
		}
		child, err := s.prober.ProveStart(d.Pid, start, daemonArg)
		if err != nil {
			d.Action = DaemonLeft
			d.Why, d.Settled = s.unprovenWhy(d.Pid, err)
			out = append(out, d)
			continue
		}
		d.Action = DaemonSignal
		if confirm {
			if err := s.prober.Signal(child, syscall.SIGTERM, false); err != nil {
				d.Why = fmt.Sprintf("signal failed: %v", err)
				var m *childproof.MismatchError
				if errors.As(err, &m) {
					// The process changed or ended between the proof and the signal.
					d.Why, d.Settled = proofWhy(err), true
				}
				log.Printf("session homes: pid %d from %s: %v", d.Pid, file, err)
			} else {
				d.Signalled, d.Settled = true, true
			}
		}
		out = append(out, d)
	}
	return out
}

// unprovenWhy says why a recorded pid is not the daemon, and whether that
// accounts for it. A pid that no longer runs, or that now belongs to another
// process, is settled. A process that exists but whose identity could not be
// read is not: it may still be the daemon.
func (s homeSweeper) unprovenWhy(pid int, err error) (why string, settled bool) {
	var m *childproof.MismatchError
	if !errors.As(err, &m) {
		return err.Error(), false
	}
	if m.Reason == "dead" {
		if exists, perr := s.prober.Exists(pid); perr != nil || exists {
			return "the process exists but its identity could not be read", false
		}
	}
	return proofWhy(err), true
}

func proofWhy(err error) string {
	var m *childproof.MismatchError
	if errors.As(err, &m) {
		switch m.Reason {
		case "dead":
			return "not running"
		case "start":
			return "the pid now belongs to a different process (start time differs)"
		case "argv":
			return "the process is not an app-server"
		}
	}
	return err.Error()
}

// readPidRecord reads one pid file under a home. absent is true for a file
// that is not there. Otherwise it returns the record, or the reason the file
// is not used: a link, a non-regular file, an oversize file, bad JSON, or a
// pid that names no single process.
func readPidRecord(file string) (rec codexPidRecord, why string, absent bool) {
	switch fi, err := os.Lstat(filepath.Dir(file)); {
	case os.IsNotExist(err):
		return rec, "", true
	case err != nil:
		return rec, err.Error(), false
	case !fi.IsDir():
		return rec, "its app-server-daemon entry is not a plain directory", false
	}
	fi, err := os.Lstat(file)
	switch {
	case os.IsNotExist(err):
		return rec, "", true
	case err != nil:
		return rec, err.Error(), false
	case !fi.Mode().IsRegular():
		return rec, "the pid file is not a regular file", false
	case fi.Size() > maxPidFileBytes:
		return rec, "the pid file is larger than a pid file should be", false
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return rec, err.Error(), false
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		return rec, "the pid file is not the JSON codex writes", false
	}
	if rec.Pid <= 1 || rec.Pid > math.MaxInt32 {
		return rec, fmt.Sprintf("the recorded pid %d names no single process", rec.Pid), false
	}
	return rec, "", false
}

// countOtherLayouts counts, read-only, the temp bases of other layouts and
// the homes inside them.
func (s homeSweeper) countOtherLayouts() (bases, homes int) {
	if s.tmpDir == "" {
		return 0, 0
	}
	entries, err := os.ReadDir(s.tmpDir)
	if err != nil {
		return 0, 0
	}
	for _, e := range entries {
		if !otherBaseRE.MatchString(e.Name()) || e.Name() == s.ownTmpBase {
			continue
		}
		bases++
		inner, err := os.ReadDir(filepath.Join(s.tmpDir, e.Name()))
		if err != nil {
			continue
		}
		for _, h := range inner {
			if homeNameRE.MatchString(h.Name()) && h.IsDir() {
				homes++
			}
		}
	}
	return bases, homes
}
