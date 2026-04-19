package rlog

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestClock returns an Options.now that advances one second per call —
// rotated filenames embed timestamps, so tests need distinct ones to
// keep lexicographic ordering meaningful and avoid clobbering archives
// created in the same wall-clock second.
func newTestClock(start time.Time) func() time.Time {
	mu := sync.Mutex{}
	t := start
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		out := t
		t = t.Add(time.Second)
		return out
	}
}

func TestWriterRotatesAtMaxSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	w, err := Open(path, Options{
		MaxFileBytes: 64,
		MaxFiles:     10,
		now:          newTestClock(time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	// Two writes each larger than the threshold — each should trigger
	// rotation after it lands.
	for i := 0; i < 2; i++ {
		payload := fmt.Sprintf("line-%d %s\n", i, strings.Repeat("x", 80))
		if _, err := w.Write([]byte(payload)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	archives, err := listArchives(path)
	if err != nil {
		t.Fatalf("listArchives: %v", err)
	}
	if len(archives) != 2 {
		t.Fatalf("expected 2 gzipped archives, got %d", len(archives))
	}

	// First archive should decompress to line-0.
	checkArchiveContents(t, archives[0].path, "line-0")
	checkArchiveContents(t, archives[1].path, "line-1")
}

func TestWriterRetentionByCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	w, err := Open(path, Options{
		MaxFileBytes: 1, // rotate every write
		MaxFiles:     2,
		now:          newTestClock(time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	for i := 0; i < 5; i++ {
		if _, err := fmt.Fprintf(w, "line-%d\n", i); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	archives, err := listArchives(path)
	if err != nil {
		t.Fatalf("listArchives: %v", err)
	}
	if len(archives) != 2 {
		t.Fatalf("expected 2 archives after retention, got %d", len(archives))
	}
	// The two archives kept must be the newest — oldest-first delete
	// means survivors are the last two.
	checkArchiveContents(t, archives[0].path, "line-3")
	checkArchiveContents(t, archives[1].path, "line-4")
}

func TestWriterRetentionByTotalBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	// Each line ~12 bytes uncompressed; gzip with tiny input is ~30-40
	// bytes wire size. Ceiling of 100 bytes across archives + active =
	// retains only the most recent 1-2.
	w, err := Open(path, Options{
		MaxFileBytes:  1,
		MaxFiles:      100, // don't let MaxFiles kick in
		MaxTotalBytes: 100,
		now:           newTestClock(time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	for i := 0; i < 10; i++ {
		if _, err := fmt.Fprintf(w, "line-%d\n", i); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	archives, err := listArchives(path)
	if err != nil {
		t.Fatalf("listArchives: %v", err)
	}
	activeSize := int64(0)
	if info, err := os.Stat(path); err == nil {
		activeSize = info.Size()
	}
	total := activeSize
	for _, a := range archives {
		total += a.size
	}
	if total > 100 {
		t.Fatalf("total bytes %d exceeds cap 100 (archives=%d)", total, len(archives))
	}
}

func TestWriterShipperCalledOnRotate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	shipped := []string{}
	var mu sync.Mutex
	w, err := Open(path, Options{
		MaxFileBytes: 1,
		MaxFiles:     10,
		Shipper: shipperFunc(func(archive string) error {
			mu.Lock()
			defer mu.Unlock()
			shipped = append(shipped, archive)
			return nil
		}),
		now: newTestClock(time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	for i := 0; i < 3; i++ {
		if _, err := fmt.Fprintf(w, "line-%d\n", i); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(shipped) != 3 {
		t.Fatalf("expected Shipper called 3 times, got %d", len(shipped))
	}
	for _, s := range shipped {
		if !strings.HasSuffix(s, ".gz") {
			t.Fatalf("shipped path doesn't end .gz: %s", s)
		}
	}
}

func TestWriterEmptyRotateNoArchive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	w, err := Open(path, Options{
		MaxFileBytes: 100,
		MaxFiles:     10,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	// Force a rotate on an empty file — should not produce an archive.
	if err := w.Rotate(); err != nil {
		t.Fatalf("Rotate empty: %v", err)
	}
	archives, err := listArchives(path)
	if err != nil {
		t.Fatalf("listArchives: %v", err)
	}
	if len(archives) != 0 {
		t.Fatalf("empty rotate should not create archive, got %d", len(archives))
	}
}

func TestWriterAppendPreservesOnReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")

	w, err := Open(path, Options{MaxFileBytes: 0})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := w.Write([]byte("first\n")); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()

	w2, err := Open(path, Options{MaxFileBytes: 0})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := w2.Write([]byte("second\n")); err != nil {
		t.Fatal(err)
	}
	_ = w2.Close()

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "first\nsecond\n" {
		t.Fatalf("expected both lines preserved, got %q", string(b))
	}
}

type shipperFunc func(string) error

func (f shipperFunc) Ship(p string) error { return f(p) }

func checkArchiveContents(t *testing.T, archivePath, wantSubstring string) {
	t.Helper()
	f, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("open %s: %v", archivePath, err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip reader %s: %v", archivePath, err)
	}
	defer func() { _ = gz.Close() }()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, gz); err != nil {
		t.Fatalf("decompress %s: %v", archivePath, err)
	}
	if !strings.Contains(buf.String(), wantSubstring) {
		t.Fatalf("archive %s: expected to contain %q, got %q", archivePath, wantSubstring, buf.String())
	}
}
