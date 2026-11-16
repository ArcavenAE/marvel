package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// txUsage reads a claude seat's transcript and keeps the seat's output-token
// sum in the spend slice (I-477 slice 1, docs/design/get-sessions-output.md
// section 4.3). The daemon owns the dedupe, the offset and the running total:
//
//   - the path comes from findTranscript, which derives it from the session id
//     marvel assigned and the seat's config dir, so nothing comes from the seat;
//   - usage counts once per message id, at the largest figure seen for it, and
//     only the growth is added, so a total is never recomputed from records;
//   - the first sight of a file with history starts at its end and marks the sum
//     partial; a shrunk file or a changed inode resets the offset, never the
//     total, and marks it partial; a daemon restart keeps the stored sum and
//     marks it partial, because the per-id figures lived in memory;
//   - the main transcript only: subagent transcripts are not counted;
//   - a client workspace is not read, as the watchdog does not read it.
//
// It keeps fresh input, cache reads, cache writes and their sum (the prompt)
// beside the output sum, each counted like it (I-477 slice 2). It writes only a
// seat the usage accountant has not metered, and no rate. A seat is the
// accountant's when it has a prompt sum and no place in a transcript: the reader
// always writes the two together, so a seat with a place is its own.
type txUsage struct {
	store *api.Store
	// client reports whether a workspace is a client one; such a seat is not
	// read, and nil counts every workspace as client.
	client func(workspace string) (bool, error)
	// tailRead returns a transcript's bytes from an offset to its end, at most
	// usageReadCap of them.
	tailRead func(path string, from int64) ([]byte, error)
	now      func() time.Time
	// write stores a seat's spend slice. It is the store's UpdateSessionSpend;
	// a test replaces it to count the writes.
	write func(key string, sp api.SessionSpend)

	seats map[string]*usageTail
}

// usageReadCap bounds one read of a transcript. A pass that finds more reads
// the rest on the next one.
const usageReadCap = 8 << 20

// usageMaxTokens is the largest output figure one record may carry; anything
// above it is not a count.
const usageMaxTokens = 1 << 31

// usageTail is the reader's memory of one seat's transcript.
type usageTail struct {
	// seeded is true when the store held a sum from a previous daemon life, so
	// the per-id figures are rebuilt from the file; stored is the place that
	// life left with it, nil when an older daemon wrote the sum without one.
	seeded bool
	stored *api.SpendTail
	// located is true once the file has been found; path and inode are its.
	located bool
	path    string
	inode   uint64
	offset  int64
	// size and mtime are the file's as of the last read, so an unchanged file
	// is not read again.
	size  int64
	mtime time.Time

	// peak is each message id's largest output figure seen.
	peak    map[string]int
	total   int
	partial bool

	// figs is each message id's largest input class figures seen, and classes the
	// running sum of their growth.
	figs    map[string]usageClasses
	classes usageClasses
}

// usageClasses are the three prompt classes of a record or of a sum: fresh input,
// cache reads and cache writes.
type usageClasses struct{ in, read, write int }

func (c usageClasses) prompt() int { return c.in + c.read + c.write }

// accountantOwned reports a seat whose spend the usage accountant writes: it has
// a prompt sum and no place in a transcript.
func accountantOwned(s api.Session) bool {
	return s.SpendPromptTokens != nil && s.SpendTail == nil
}

func newTxUsage(store *api.Store, client func(string) (bool, error)) *txUsage {
	u := &txUsage{store: store, client: client, tailRead: readUsageTail, now: time.Now, seats: map[string]*usageTail{}}
	u.write = store.UpdateSessionSpend
	return u
}

// readUsageTail returns the bytes of path from an offset, at most usageReadCap.
func readUsageTail(path string, from int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(f, usageReadCap))
}

// Run passes every WatchdogInterval until ctx is cancelled. A suspended
// cluster is not read.
func (u *txUsage) Run(ctx context.Context, suspended func() bool) {
	t := time.NewTicker(WatchdogInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if suspended != nil && suspended() {
				continue
			}
			u.Once()
		}
	}
}

// Once runs one pass over every running claude seat.
func (u *txUsage) Once() {
	seen := map[string]bool{}
	for _, s := range u.store.ListSessions() {
		key := s.Key()
		if !u.readable(s) {
			continue
		}
		seen[key] = true
		u.visit(key, s)
	}
	for k := range u.seats {
		if !seen[k] {
			delete(u.seats, k)
		}
	}
}

// readable reports whether a seat's transcript may be read at all.
func (u *txUsage) readable(s api.Session) bool {
	if s.State != api.SessionRunning || s.Runtime.Name != "claude" {
		return false
	}
	// The accountant owns the slice once it has metered a request.
	if accountantOwned(s) {
		return false
	}
	id := s.HarnessSessionID
	if id == "" || strings.ContainsAny(id, "/\\\x00") {
		return false
	}
	if u.client == nil {
		return false
	}
	if c, err := u.client(s.Workspace); err != nil || c {
		return false
	}
	return true
}

func (u *txUsage) visit(key string, s api.Session) {
	t := u.seats[key]
	first := t == nil
	if first {
		t = u.newTail(s)
		u.seats[key] = t
	}
	path, why := findTranscript(s, s.HarnessSessionID)
	if why != "" {
		return // no file yet: what appears later is read from byte 0 and is exact
	}
	fi, err := os.Stat(path)
	if err != nil {
		return
	}
	ino := inodeOf(fi)
	switch {
	case !t.located:
		t.located, t.path, t.inode = true, path, ino
		if t.seeded {
			// A sum from a previous daemon life: rebuild the per-id figures, also
			// when the file only appeared after the first pass.
			t.seeded = false
			u.seed(t, path, ino, fi.Size())
		} else if first && fi.Size() > 0 {
			// History already there at first sight: start at its end, so the
			// sum is a lower bound.
			t.offset, t.partial = fi.Size(), true
		}
		u.publish(key, t)
	case ino != t.inode || path != t.path || fi.Size() < t.offset:
		// Replaced or truncated: the offset restarts and the sum is a lower
		// bound from here on. The per-id figures stay, so a re-read adds nothing
		// for an id already counted.
		t.path, t.inode, t.offset, t.partial = path, ino, 0, true
		t.size = -1
	}
	if fi.Size() == t.size && fi.ModTime().Equal(t.mtime) {
		return
	}
	from := t.offset
	data, err := u.tailRead(t.path, from)
	if err != nil {
		return
	}
	// The size and mtime are recorded only when this read reached the end of
	// the file, so a read cut at the cap is continued on the next pass.
	if from+int64(len(data)) >= fi.Size() {
		t.size, t.mtime = fi.Size(), fi.ModTime()
	}
	end := bytes.LastIndexByte(data, '\n') + 1
	if end == 0 && len(data) >= usageReadCap {
		// One record longer than a whole read: drop its head and carry on.
		end = len(data)
	}
	t.offset += int64(end)
	for _, line := range bytes.Split(data[:end], []byte{'\n'}) {
		t.count(line, true)
	}
	u.publish(key, t)
}

// newTail starts the memory of a seat. A stored sum from before a restart is
// the base, and a restart marks the sum partial.
func (u *txUsage) newTail(s api.Session) *usageTail {
	t := &usageTail{size: -1, peak: map[string]int{}, figs: map[string]usageClasses{}}
	if s.SpendOut != nil {
		t.total = *s.SpendOut
		t.partial = true
		t.seeded = true
		if s.SpendTail != nil {
			st := *s.SpendTail
			t.stored = &st
			t.partial = s.SpendPartial
		}
		if s.SpendFresh != nil && s.SpendCacheRead != nil && s.SpendCacheWrite != nil {
			t.classes = usageClasses{*s.SpendFresh, *s.SpendCacheRead, *s.SpendCacheWrite}
		} else {
			// An output sum an older daemon kept alone: the classes behind it
			// were not counted, so the prompt sum starts from here as a lower bound.
			t.partial = true
		}
	}
	return t
}

// count adds one transcript line. A line that is not an assistant record with
// a message id and a usable output figure adds nothing.
func (t *usageTail) count(line []byte, add bool) {
	if len(line) == 0 {
		return
	}
	var rec struct {
		Type    string `json:"type"`
		Message struct {
			ID    string `json:"id"`
			Usage struct {
				Output     any `json:"output_tokens"`
				Input      any `json:"input_tokens"`
				CacheRead  any `json:"cache_read_input_tokens"`
				CacheWrite any `json:"cache_creation_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &rec); err != nil || rec.Type != "assistant" || rec.Message.ID == "" {
		return
	}
	f, ok := rec.Message.Usage.Output.(float64)
	if !ok || f < 0 || f >= usageMaxTokens || f != float64(int(f)) {
		return
	}
	n := int(f)
	// A class a record leaves out is a metered zero; one that is not a count
	// makes the whole record uncountable.
	var cur usageClasses
	for _, c := range []struct {
		v   any
		dst *int
	}{{rec.Message.Usage.Input, &cur.in}, {rec.Message.Usage.CacheRead, &cur.read}, {rec.Message.Usage.CacheWrite, &cur.write}} {
		if c.v == nil {
			continue
		}
		g, ok := c.v.(float64)
		if !ok || g < 0 || g >= usageMaxTokens || g != float64(int(g)) {
			return
		}
		*c.dst = int(g)
	}
	if old := t.peak[rec.Message.ID]; n > old {
		t.peak[rec.Message.ID] = n
		if add {
			t.total += n - old
		}
	}
	old := t.figs[rec.Message.ID]
	peak := usageClasses{max(old.in, cur.in), max(old.read, cur.read), max(old.write, cur.write)}
	t.figs[rec.Message.ID] = peak
	if add {
		t.classes.in += peak.in - old.in
		t.classes.read += peak.read - old.read
		t.classes.write += peak.write - old.write
	}
}

// publish writes the seat's sum when it changed, and leaves the prompt sum and
// the rate as they are.
func (u *txUsage) publish(key string, t *usageTail) {
	cur, err := u.store.GetSession(key)
	if err != nil {
		return
	}
	// The accountant may have metered the seat since this pass began.
	if accountantOwned(cur) {
		return
	}
	var tail *api.SpendTail
	if t.located {
		tail = &api.SpendTail{Offset: t.offset, Inode: t.inode}
	}
	sameTail := (tail == nil && cur.SpendTail == nil) || (tail != nil && cur.SpendTail != nil && *tail == *cur.SpendTail)
	sameClasses := cur.SpendFresh != nil && cur.SpendCacheRead != nil && cur.SpendCacheWrite != nil && cur.SpendPromptTokens != nil &&
		(usageClasses{*cur.SpendFresh, *cur.SpendCacheRead, *cur.SpendCacheWrite}) == t.classes
	if cur.SpendOut != nil && *cur.SpendOut == t.total && sameClasses && cur.SpendPartial == t.partial && sameTail {
		return
	}
	out, fresh, read, write, prompt := t.total, t.classes.in, t.classes.read, t.classes.write, t.classes.prompt()
	u.write(key, api.SessionSpend{Out: &out, PromptTokens: &prompt, Fresh: &fresh, CacheRead: &read, CacheWrite: &write, OutRate: cur.OutRate, Partial: t.partial, Tail: tail})
}

// startTxUsage runs the transcript usage reader beside the watchdog. It reads
// no pane, so it does not wait on the watchdog's pattern sets.
func (d *Daemon) startTxUsage(ctx context.Context) {
	u := newTxUsage(d.store, d.workspaceIsClient)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		u.Run(ctx, d.clusterSuspended)
	}()
}

// seed rebuilds the per-id figures of a seat the daemon already had a sum for,
// adding nothing to the total. When the file is the one the store's place was
// recorded in and has not shrunk, it reads up to that place and the sum stays
// exact. Otherwise it reads the whole file, puts the offset at its end and marks
// the sum partial: records only in the new file are not counted.
func (u *txUsage) seed(t *usageTail, path string, ino uint64, size int64) {
	upto := size
	same := t.stored != nil && t.stored.Offset >= 0 && ino == t.stored.Inode && size >= t.stored.Offset
	if same {
		upto = t.stored.Offset
	}
	var off int64
	for off < upto {
		data, err := u.tailRead(path, off)
		if err != nil || len(data) == 0 {
			break
		}
		if room := upto - off; int64(len(data)) > room {
			data = data[:room]
		}
		end := bytes.LastIndexByte(data, '\n') + 1
		if end == 0 {
			break
		}
		for _, line := range bytes.Split(data[:end], []byte{'\n'}) {
			t.count(line, false)
		}
		off += int64(end)
	}
	t.offset = off
	if !same || off < t.stored.Offset {
		t.partial = true
	}
}
