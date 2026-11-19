package api

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
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

// seriesStore is the disk side of an AccountSeries. Its zero value is no store:
// a series that was never attached writes nothing.
type seriesStore struct {
	dir       string
	retention time.Duration
	pruneDay  string // the UTC day the last prune ran for
	failed    bool   // the last write failed; log the first failure, not each
}

// Attach makes the series durable. It creates dir, removes day files whose day
// ended more than retention before now (a retention of zero or less means
// DefaultPersistRetention), reads the day files that can hold a reading inside
// the memory window back into the series, and from then on appends every
// reading the series keeps to the file of its UTC day, mode 0600.
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
	if err := pruneSeriesDays(dir, retention, now); err != nil {
		return err
	}
	if err := s.loadDays(dir, now); err != nil {
		return err
	}
	s.store = seriesStore{dir: dir, retention: retention, pruneDay: now.UTC().Format(seriesDayLayout)}
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
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		skipped := 0
		for sc.Scan() {
			var ln seriesLine
			if err := json.Unmarshal(sc.Bytes(), &ln); err != nil || ln.Sample.Window == "" || ln.Sample.At.IsZero() {
				skipped++
				continue
			}
			if ln.Sample.At.Before(cutoff) {
				continue
			}
			s.insert(ln.Key, ln.Sample)
		}
		if err := sc.Err(); err != nil {
			skipped++
		}
		_ = f.Close()
		if skipped > 0 {
			log.Printf("account series: %s: skipped %d unreadable line(s)", e.Name(), skipped)
		}
	}
	return nil
}

// persist appends one kept reading to its day file and, the first time a new
// UTC day is seen, removes day files past the retention. A failure is logged
// and the reading stays in memory: the series answers from memory either way.
// The caller holds s.mu.
func (s *AccountSeries) persist(key AccountKey, smp AccountSample) {
	if s.store.dir == "" {
		return
	}
	line, err := json.Marshal(seriesLine{Key: key, Sample: smp})
	if err == nil {
		err = s.store.append(smp.At, append(line, '\n'))
	}
	if err != nil {
		if !s.store.failed {
			log.Printf("account series: not persisting readings: %v", err)
		}
		s.store.failed = true
		return
	}
	s.store.failed = false
	if day := smp.At.UTC().Format(seriesDayLayout); day != s.store.pruneDay {
		s.store.pruneDay = day
		if err := pruneSeriesDays(s.store.dir, s.store.retention, smp.At); err != nil {
			log.Printf("account series: %v", err)
		}
	}
}

func (st *seriesStore) append(at time.Time, b []byte) error {
	name := filepath.Join(st.dir, at.UTC().Format(seriesDayLayout)+seriesDaySuffix)
	f, err := os.OpenFile(name, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(b)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}
