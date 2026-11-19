package api

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultPersistRetention is how long a day file of readings is kept on disk:
// 90 days, the operator's ruling on retention. The memory cache stays at
// DefaultSeriesRetention; the files are what a later reader (a quota-per-token
// figure over a quarter) reads past it.
const DefaultPersistRetention = 90 * 24 * time.Hour

const (
	seriesDayLayout = "2006-01-02"
	seriesDaySuffix = ".jsonl"
)

// seriesLine is one line of a day file. The key is the whole AccountKey, not
// its rendering: String drops nothing today, but a line must be able to put a
// sample back under the key it was taken for.
type seriesLine struct {
	Key    AccountKey    `json:"key"`
	Sample AccountSample `json:"sample"`
}

// seriesStore is the disk side of an AccountSeries. A series that was never
// attached has none and writes nothing. It has its own lock, so a write that is
// slow does not hold up a reader of the series.
type seriesStore struct {
	dir       string
	retention time.Duration

	mu       sync.Mutex
	pruneDay string // the UTC day the last prune ran for
	failed   bool   // the last write failed; log the first failure, not each

	// beforeWrite, when set by a test, runs just before a line is written.
	beforeWrite func()
}

// Attach makes the series durable. It creates dir, removes day files whose day
// ended more than retention before now, reads the day files that can hold a
// reading inside the memory window back into the series, and from then on
// appends every reading the series keeps to the file of its UTC day, mode 0600.
//
// A retention of zero or less means DefaultPersistRetention, and a retention
// shorter than the memory window is raised to it, so a short setting cannot
// shorten the window a restart reads back.
//
// A line that does not parse is skipped and logged, so a file cut short by a
// crash costs one reading and not the start. An error is returned only when dir
// cannot be made or listed.
func (s *AccountSeries) Attach(dir string, retention time.Duration, now time.Time) error {
	if retention <= 0 {
		retention = DefaultPersistRetention
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("account series dir: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if retention < s.retention {
		retention = s.retention
	}
	if err := pruneSeriesDays(dir, retention, now); err != nil {
		return err
	}
	if err := s.loadDays(dir, now); err != nil {
		return err
	}
	s.store = &seriesStore{dir: dir, retention: retention, pruneDay: now.UTC().Format(seriesDayLayout)}
	return nil
}

// dayOf parses a day file's name, and reports whether it is one.
func dayOf(name string) (time.Time, bool) {
	d, err := time.Parse(seriesDayLayout, strings.TrimSuffix(name, seriesDaySuffix))
	if err != nil || !strings.HasSuffix(name, seriesDaySuffix) {
		return time.Time{}, false
	}
	return d, true
}

// pruneSeriesDays removes the day files that end before now-retention. A file
// is judged by the end of its day, so a file that still holds a reading inside
// the retention is never removed.
func pruneSeriesDays(dir string, retention time.Duration, now time.Time) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("account series dir: %w", err)
	}
	cutoff := now.UTC().Add(-retention)
	for _, e := range ents {
		day, ok := dayOf(e.Name())
		if !ok || e.IsDir() {
			continue
		}
		if day.Add(24 * time.Hour).After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("account series: remove %s: %v", e.Name(), err)
		}
	}
	return nil
}

// loadDays reads the day files that can hold a reading no older than the memory
// retention at now, and keeps the readings that are inside it. The caller holds
// s.mu.
func (s *AccountSeries) loadDays(dir string, now time.Time) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("account series dir: %w", err)
	}
	cutoff := now.UTC().Add(-s.retention)
	for _, e := range ents {
		day, ok := dayOf(e.Name())
		if !ok || e.IsDir() || !day.Add(24*time.Hour).After(cutoff) {
			continue
		}
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			log.Printf("account series: open %s: %v", e.Name(), err)
			continue
		}
		skipped := s.loadLines(f, cutoff)
		_ = f.Close()
		if skipped > 0 {
			log.Printf("account series: %s: skipped %d unreadable line(s)", e.Name(), skipped)
		}
	}
	return nil
}

// seriesMaxLine is the longest line loadLines will hold. A real line is a few
// hundred bytes; a longer one is damage, and is skipped without being held.
const seriesMaxLine = 1 << 20

// loadLines reads one day file line by line and keeps the readings at or after
// cutoff. A line of any length is one line: a corrupt or overlong one is counted
// and skipped, and the lines after it are still read. The caller holds s.mu.
func (s *AccountSeries) loadLines(r io.Reader, cutoff time.Time) (skipped int) {
	br := bufio.NewReaderSize(r, 64*1024)
	var line []byte
	tooLong := false
	for {
		part, isPrefix, err := br.ReadLine()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				skipped++
			}
			return skipped
		}
		if !tooLong {
			if len(line)+len(part) > seriesMaxLine {
				tooLong = true
			} else {
				line = append(line, part...)
			}
		}
		if isPrefix {
			continue
		}
		if tooLong {
			skipped++
		} else if len(line) > 0 {
			var ln seriesLine
			switch {
			case json.Unmarshal(line, &ln) != nil || ln.Sample.Window == "" || ln.Sample.At.IsZero():
				skipped++
			case ln.Sample.At.Before(cutoff):
			default:
				s.insert(ln.Key, ln.Sample)
			}
		}
		line, tooLong = line[:0], false
	}
}

// write appends kept readings to their day files and, the first time a new UTC
// day is seen, removes day files past the retention. A failure is logged and
// the readings stay in memory: the series answers from memory either way. It is
// called without the series lock.
func (st *seriesStore) write(lines []seriesLine) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, ln := range lines {
		b, err := json.Marshal(ln)
		if err == nil {
			err = st.append(ln.Sample.At, append(b, '\n'))
		}
		if err != nil {
			if !st.failed {
				log.Printf("account series: not persisting readings: %v", err)
			}
			st.failed = true
			continue
		}
		st.failed = false
		if day := ln.Sample.At.UTC().Format(seriesDayLayout); day != st.pruneDay {
			st.pruneDay = day
			if err := pruneSeriesDays(st.dir, st.retention, ln.Sample.At); err != nil {
				log.Printf("account series: %v", err)
			}
		}
	}
}

// append writes one line to the file of the UTC day of at. When the file ends
// without a newline (a crash or a full disk cut the last write short), the line
// starts after one, so the torn line stays one bad line and the new reading is
// its own.
func (st *seriesStore) append(at time.Time, b []byte) error {
	if st.beforeWrite != nil {
		st.beforeWrite()
	}
	name := filepath.Join(st.dir, at.UTC().Format(seriesDayLayout)+seriesDaySuffix)
	f, err := os.OpenFile(name, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if fi, serr := f.Stat(); serr == nil && fi.Size() > 0 {
		last := make([]byte, 1)
		if _, rerr := f.ReadAt(last, fi.Size()-1); rerr == nil && last[0] != '\n' {
			b = append([]byte{'\n'}, b...)
		}
	}
	_, werr := f.Write(b)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}
