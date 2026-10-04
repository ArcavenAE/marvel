package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// The Claude Code statusline carries no data time: its rate_limits block has a
// used_percentage and a resets_at per window and nothing that says when the
// figure was true. Stamping the send time would make every re-render of an old
// figure look new, and a sibling seat re-sending a stale 40 would displace a
// fresh 100. So the forwarder stamps the time it FIRST SAW this figure for this
// seat, and keeps that stamp while the figure is unchanged. A changed figure is
// news and takes a new stamp (marvel#551 review).

type stampFile struct {
	Figure    string    `json:"figure"`
	FirstSeen time.Time `json:"first_seen"`
}

// figureOf fingerprints the figure: every window's name, percentage and reset.
func figureOf(windows []api.AccountWindow) string {
	var b strings.Builder
	for _, w := range windows {
		pct := -1.0
		if w.UsedPercent != nil {
			pct = *w.UsedPercent
		}
		fmt.Fprintf(&b, "%s|%v|%d;", w.Name, pct, w.ResetsAt.Unix())
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// observationStamp returns when this seat first saw this figure, recording now
// when it is new. dir is a directory the seat can write (the one holding its
// daemon socket). On any failure it returns the zero time, which the daemon
// reads as unknown and which never displaces a stamped reading: failing to
// remember must not turn into stamping everything as new.
func observationStamp(dir, workspace, session string, windows []api.AccountWindow, now time.Time) time.Time {
	if dir == "" || len(windows) == 0 {
		return time.Time{}
	}
	path := filepath.Join(dir, stampFileName(workspace, session))
	figure := figureOf(windows)
	if raw, err := os.ReadFile(path); err == nil {
		var prev stampFile
		if json.Unmarshal(raw, &prev) == nil && prev.Figure == figure && !prev.FirstSeen.IsZero() && !prev.FirstSeen.After(now) {
			return prev.FirstSeen
		}
	}
	raw, err := json.Marshal(stampFile{Figure: figure, FirstSeen: now.UTC()})
	if err != nil {
		return time.Time{}
	}
	// A fresh temp file in the same directory (O_EXCL, a random name), so a
	// planted symlink at a guessable name is never followed; then rename over.
	f, err := os.CreateTemp(dir, ".marvel-acct-*.tmp")
	if err != nil {
		return time.Time{}
	}
	tmp := f.Name()
	_, werr := f.Write(raw)
	cerr := f.Close()
	if werr != nil || cerr != nil || os.Chmod(tmp, 0o600) != nil {
		_ = os.Remove(tmp)
		return time.Time{}
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return time.Time{}
	}
	return now.UTC()
}

// stampFileName names a seat's stamp file by a hash of the pair, with a NUL
// between the parts, so no two (workspace, session) pairs share a file however
// they are spelled: "a-b"+"c" and "a"+"b-c" differ, as do "x.y" and "x_y".
func stampFileName(workspace, session string) string {
	sum := sha256.Sum256([]byte(workspace + "\x00" + session))
	return ".marvel-acct-" + hex.EncodeToString(sum[:12]) + ".json"
}
