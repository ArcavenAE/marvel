// Package rlog provides a size-rotating, compressing log writer for the
// marvel daemon's on-disk log file. Designed for low-quota environments
// like the desk Pi — bounds total disk usage, gzip-compresses rolled
// files, and surfaces a Shipper hook so operators can offload archives
// elsewhere when they care to.
//
// Usage:
//
//	w, err := rlog.Open(path, rlog.Options{
//	    MaxFileBytes: 10 * 1024 * 1024, // 10 MiB
//	    MaxFiles:     5,                // keep 5 gzipped archives
//	})
//	if err != nil { ... }
//	defer w.Close()
//	log.SetOutput(w)
//
// The writer is safe for concurrent Write calls.
package rlog

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Options configures a rotating Writer. Zero values mean "no limit" for
// size-bounded fields, and "no retention" for count-bounded fields —
// pick sane values explicitly at the call site.
type Options struct {
	// MaxFileBytes is the size threshold at which the active log file is
	// rotated. Zero disables size-based rotation (file grows unbounded).
	MaxFileBytes int64

	// MaxFiles is the number of gzipped rotated files to keep alongside
	// the active one. Zero keeps all of them (no count-based deletion).
	MaxFiles int

	// MaxTotalBytes is the disk-usage ceiling across the active file plus
	// all rotated archives. When exceeded, the oldest archives are
	// deleted until the total is under the ceiling again. Zero disables.
	MaxTotalBytes int64

	// Mode is the file mode used when creating the active log file. Zero
	// falls back to 0600.
	Mode os.FileMode

	// Shipper, when non-nil, is invoked with the path of each newly
	// rotated (and gzipped) archive before retention runs. Shipper
	// errors are logged via the writer's ErrorLog but do not block
	// rotation — archives are deleted on retention even if Ship failed.
	Shipper Shipper

	// ErrorLog is where the writer reports background failures
	// (shipping, retention). When nil, errors are written to stderr.
	ErrorLog func(format string, args ...any)

	// now is an injection point for tests; nil means time.Now().UTC().
	now func() time.Time
}

// Shipper offloads a rotated archive to some external destination.
// Implementations might upload to S3, scp to a central logger, or copy
// to a mounted filesystem. The stub [NoopShipper] does nothing.
//
// Ship is called synchronously from the writer's rotation path. Keep
// implementations fast or run the actual transfer asynchronously and
// return nil immediately.
type Shipper interface {
	Ship(archivePath string) error
}

// NoopShipper implements Shipper and does nothing. Use this to reserve
// the hook without yet deciding where archives go.
type NoopShipper struct{}

// Ship satisfies the Shipper interface.
func (NoopShipper) Ship(string) error { return nil }

// Writer is a size-rotating, gzip-compressing io.WriteCloser. Safe for
// concurrent Write calls.
type Writer struct {
	path string
	opts Options

	mu      sync.Mutex
	file    *os.File
	written int64 // bytes written to the active file since open
}

// Open creates (or appends to) a rotating log at path with the given
// options. Existing content in the file is preserved and its size is
// consulted so Open after a restart doesn't reset the rotation counter.
func Open(path string, opts Options) (*Writer, error) {
	if opts.Mode == 0 {
		opts.Mode = 0o600
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, opts.Mode)
	if err != nil {
		return nil, fmt.Errorf("open log %s: %w", path, err)
	}
	_ = os.Chmod(path, opts.Mode)
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("stat log %s: %w", path, err)
	}
	return &Writer{
		path:    path,
		opts:    opts,
		file:    f,
		written: info.Size(),
	}, nil
}

// Write satisfies io.Writer. Rotation is decided *after* a write, so a
// single line never gets split across files — the one that trips the
// size threshold lands in the old file, then rotation happens before
// the next Write.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return 0, os.ErrClosed
	}
	n, err := w.file.Write(p)
	w.written += int64(n)
	if err != nil {
		return n, err
	}
	if w.opts.MaxFileBytes > 0 && w.written >= w.opts.MaxFileBytes {
		if rerr := w.rotateLocked(); rerr != nil {
			w.logErr("rotate %s: %v", w.path, rerr)
		}
	}
	return n, nil
}

// Close closes the active file. The writer is unusable afterward.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// Rotate forces a rotation even if the size threshold hasn't been
// reached. Useful for scheduled cuts (e.g., daily) or tests.
func (w *Writer) Rotate() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rotateLocked()
}

func (w *Writer) rotateLocked() error {
	if w.file == nil {
		return os.ErrClosed
	}
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("close active: %w", err)
	}
	w.file = nil

	archivePath := ""
	// Only bother rotating a non-empty file — rotating an empty file
	// creates a zero-byte archive every time Rotate is called on a
	// freshly-opened writer, which is noise.
	if info, err := os.Stat(w.path); err == nil && info.Size() > 0 {
		ts := w.now().Format("20060102T150405Z")
		archivePath = fmt.Sprintf("%s.%s.gz", w.path, ts)
		if err := compressAndReplace(w.path, archivePath); err != nil {
			return fmt.Errorf("compress: %w", err)
		}
	}

	f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, w.opts.Mode)
	if err != nil {
		return fmt.Errorf("reopen active: %w", err)
	}
	_ = os.Chmod(w.path, w.opts.Mode)
	w.file = f
	w.written = 0

	if archivePath != "" && w.opts.Shipper != nil {
		if err := w.opts.Shipper.Ship(archivePath); err != nil {
			w.logErr("ship %s: %v", archivePath, err)
		}
	}

	if err := w.enforceRetentionLocked(); err != nil {
		w.logErr("retention: %v", err)
	}
	return nil
}

// compressAndReplace gzips src into dst, then removes src. Uses a
// temporary file to avoid leaving a half-written archive if the process
// dies mid-compression.
func compressAndReplace(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup if something goes wrong before the rename.
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	gz := gzip.NewWriter(tmp)
	if _, err := io.Copy(gz, in); err != nil {
		_ = gz.Close()
		_ = tmp.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return err
	}
	cleanup = false
	return os.Remove(src)
}

// enforceRetentionLocked applies MaxFiles and MaxTotalBytes caps,
// deleting the oldest archives (by embedded timestamp) first. Caller
// must hold w.mu.
func (w *Writer) enforceRetentionLocked() error {
	if w.opts.MaxFiles <= 0 && w.opts.MaxTotalBytes <= 0 {
		return nil
	}
	archives, err := listArchives(w.path)
	if err != nil {
		return err
	}
	// archives is sorted oldest-first (ascending timestamp).
	if w.opts.MaxFiles > 0 {
		for len(archives) > w.opts.MaxFiles {
			if err := os.Remove(archives[0].path); err != nil {
				return fmt.Errorf("delete %s: %w", archives[0].path, err)
			}
			archives = archives[1:]
		}
	}
	if w.opts.MaxTotalBytes > 0 {
		// Include the active file in the total.
		activeSize := int64(0)
		if info, err := os.Stat(w.path); err == nil {
			activeSize = info.Size()
		}
		total := activeSize
		for _, a := range archives {
			total += a.size
		}
		for total > w.opts.MaxTotalBytes && len(archives) > 0 {
			if err := os.Remove(archives[0].path); err != nil {
				return fmt.Errorf("delete %s: %w", archives[0].path, err)
			}
			total -= archives[0].size
			archives = archives[1:]
		}
	}
	return nil
}

type archiveInfo struct {
	path string
	size int64
}

// listArchives returns all rotated archives for basePath, sorted by the
// timestamp embedded in the filename (oldest first).
func listArchives(basePath string) ([]archiveInfo, error) {
	dir := filepath.Dir(basePath)
	prefix := filepath.Base(basePath) + "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var archives []archiveInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".gz") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		archives = append(archives, archiveInfo{
			path: filepath.Join(dir, name),
			size: info.Size(),
		})
	}
	// Filename embeds a lexicographically-sortable timestamp, so string
	// sort == chronological sort.
	sort.Slice(archives, func(i, j int) bool {
		return archives[i].path < archives[j].path
	})
	return archives, nil
}

func (w *Writer) now() time.Time {
	if w.opts.now != nil {
		return w.opts.now()
	}
	return time.Now().UTC()
}

func (w *Writer) logErr(format string, args ...any) {
	if w.opts.ErrorLog != nil {
		w.opts.ErrorLog(format, args...)
		return
	}
	fmt.Fprintf(os.Stderr, "rlog: "+format+"\n", args...)
}
