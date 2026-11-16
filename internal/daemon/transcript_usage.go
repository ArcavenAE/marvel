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
// It writes only a seat the usage accountant has not metered (SpendPromptTokens
// nil), and it writes no prompt tokens and no rate.
type txUsage struct {
	store *api.Store
	// client reports whether a workspace is a client one; such a seat is not
	// read, and nil counts every workspace as client.
	client func(workspace string) (bool, error)
	// tailRead returns a transcript's bytes from an offset to its end, at most
	// usageReadCap of them.
	tailRead func(path string, from int64) ([]byte, error)
	now      func() time.Time

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
}

func newTxUsage(store *api.Store, client func(string) (bool, error)) *txUsage {
	return &txUsage{store: store, client: client, tailRead: readUsageTail, now: time.Now, seats: map[string]*usageTail{}}
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
	if s.SpendPromptTokens != nil {
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
		if first && fi.Size() > 0 {
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
	data, err := u.tailRead(t.path, t.offset)
	if err != nil {
		return
	}
	t.size, t.mtime = fi.Size(), fi.ModTime()
	end := bytes.LastIndexByte(data, '\n') + 1
	if end == 0 && len(data) >= usageReadCap {
		// One record longer than a whole read: drop its head and carry on.
		end = len(data)
	}
	t.offset += int64(end)
	for _, line := range bytes.Split(data[:end], []byte{'\n'}) {
		t.count(line)
	}
	u.publish(key, t)
}

// newTail starts the memory of a seat. A stored sum from before a restart is
// the base, and a restart marks the sum partial.
func (u *txUsage) newTail(s api.Session) *usageTail {
	t := &usageTail{size: -1, peak: map[string]int{}}
	if s.SpendOut != nil {
		t.total = *s.SpendOut
		t.partial = true
	}
	return t
}

// count adds one transcript line. A line that is not an assistant record with
// a message id and a usable output figure adds nothing.
func (t *usageTail) count(line []byte) {
	if len(line) == 0 {
		return
	}
	var rec struct {
		Type    string `json:"type"`
		Message struct {
			ID    string `json:"id"`
			Usage struct {
				Output any `json:"output_tokens"`
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
	if old := t.peak[rec.Message.ID]; n > old {
		t.peak[rec.Message.ID] = n
		t.total += n - old
	}
}

// publish writes the seat's sum when it changed, and leaves the prompt sum and
// the rate as they are.
func (u *txUsage) publish(key string, t *usageTail) {
	cur, err := u.store.GetSession(key)
	if err != nil {
		return
	}
	if cur.SpendOut != nil && *cur.SpendOut == t.total && cur.SpendPartial == t.partial {
		return
	}
	out := t.total
	u.store.UpdateSessionSpend(key, api.SessionSpend{Out: &out, PromptTokens: cur.SpendPromptTokens, OutRate: cur.OutRate, Partial: t.partial})
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
