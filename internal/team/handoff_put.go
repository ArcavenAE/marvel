package team

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// handoff.put (docs/design/read-only-seat-handoff.md, section 2). A seat that
// cannot write a file on the daemon host asks the daemon to write its handoff.
// The daemon writes the role's declared handoff file, ending with the declared
// marker, so the shift path that reads the file is unchanged (D5). The seat
// writes nothing.

// MaxHandoffPutBytes caps the text one handoff.put may carry.
const MaxHandoffPutBytes = api.MaxHandoffPutBytes

// Refusals of a handoff.put. Each is wrapped with the detail a caller or an
// event needs; callers match with errors.Is.
var (
	// ErrHandoffUnauthorized: the token is absent, unknown, or minted for a
	// record that carries no token hash (a handoff is a write, so it does not
	// inherit the heartbeat's fail-open for unbound records).
	ErrHandoffUnauthorized = errors.New("handoff.put: token does not identify a bound session")
	// ErrHandoffNotRequested: the authenticated session does not hold the
	// role's pending max-age request.
	ErrHandoffNotRequested = errors.New("handoff.put: this session holds no pending handoff request")
	// ErrHandoffNoPath: the role declares no handoff file for marvel to write.
	ErrHandoffNoPath = errors.New("handoff.put: the role declares no handoff file")
	// ErrHandoffTooLarge: the text is empty or over MaxHandoffPutBytes.
	ErrHandoffTooLarge = errors.New("handoff.put: text is empty or too large")
	// ErrHandoffDir: the handoff directory is not the directory marvel
	// recorded when it asked, or it or an ancestor is a link, or the target
	// is not a regular file.
	ErrHandoffDir = errors.New("handoff.put: handoff directory refused")
)

// HandoffPut is what a successful handoff.put reports.
type HandoffPut struct {
	// Path is the handoff file marvel wrote.
	Path string
	// Bytes is the size of the file written, marker included.
	Bytes int
}

// dirIdentity returns the device and inode of dir, following links, so that
// the identity recorded at request time is the directory the path resolves to.
func dirIdentity(dir string) (dev, ino uint64, err error) {
	var st syscall.Stat_t
	if err := syscall.Stat(dir, &st); err != nil {
		return 0, 0, err
	}
	return uint64(st.Dev), uint64(st.Ino), nil //nolint:unconvert // Dev is int32 on darwin, uint64 on linux
}

// PutHandoff writes the handoff of the session that owns token, and of no
// other. The write gate has four parts, and a put is refused, with an event
// naming the reason, unless all hold:
//
//  1. The token is bound and matches a session. The request names no session.
//  2. That session holds the role's pending request (one per role), not
//     escalated.
//  3. The path comes from the role's declared template and the stored session,
//     never from the request.
//  4. No link is followed in the file or its directory, and the directory is
//     the one recorded when marvel asked: checked on the opened handle, so a
//     swap after the check cannot redirect the write.
//
// Same-uid seats are not isolated from each other by marvel; the residuals are
// stated in the design (section 2). The text is capped, never truncated.
func (c *Controller) PutHandoff(token, text string) (HandoffPut, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	sess, got, err := c.putHandoffLocked(token, text)
	if err != nil {
		c.emitPutRefused(sess, err)
		return HandoffPut{}, err
	}
	return got, nil
}

func (c *Controller) putHandoffLocked(token, text string) (api.Session, HandoffPut, error) {
	sess, err := c.store.SessionByHeartbeatToken(token)
	if err != nil {
		return api.Session{}, HandoffPut{}, ErrHandoffUnauthorized
	}
	if !sess.State.CountsAsAlive() {
		return sess, HandoffPut{}, fmt.Errorf("%w: session %s is %s", ErrHandoffNotRequested, sess.Key(), sess.State)
	}
	if strings.TrimSpace(text) == "" || len(text) > MaxHandoffPutBytes {
		return sess, HandoffPut{}, fmt.Errorf("%w: %d bytes, limit %d", ErrHandoffTooLarge, len(text), MaxHandoffPutBytes)
	}

	teamKey := sess.Workspace + "/" + sess.Team
	t, err := c.store.GetTeam(teamKey)
	if err != nil {
		return sess, HandoffPut{}, fmt.Errorf("%w: team %s: %v", ErrHandoffNotRequested, teamKey, err)
	}
	var role *api.Role
	for i := range t.Roles {
		if t.Roles[i].Name == sess.Role {
			role = &t.Roles[i]
			break
		}
	}
	if role == nil {
		return sess, HandoffPut{}, fmt.Errorf("%w: role %s is not in team %s", ErrHandoffNotRequested, sess.Role, teamKey)
	}
	req, ok := t.ShiftRequests[role.Name]
	if !ok || req.Session != sess.Key() || req.Escalated {
		return sess, HandoffPut{}, fmt.Errorf("%w: role %s", ErrHandoffNotRequested, role.Name)
	}
	if role.Shift == nil || role.Shift.Handoff == "" || role.Shift.HandoffMarker == "" {
		return sess, HandoffPut{}, fmt.Errorf("%w: role %s", ErrHandoffNoPath, role.Name)
	}
	path, err := handoffPath(role.Shift.Handoff, sess)
	if err != nil {
		return sess, HandoffPut{}, fmt.Errorf("%w: %v", ErrHandoffNoPath, err)
	}

	body := handoffBody(text, role.Shift.HandoffMarker)
	if err := writeHandoffFile(path, req, body); err != nil {
		return sess, HandoffPut{}, err
	}

	now := c.nowUTC()
	if err := c.store.UpdateTeam(teamKey, func(live *api.Team) error {
		if r, ok := live.ShiftRequests[role.Name]; ok && r.Session == sess.Key() {
			r.PutAt = now
			live.ShiftRequests[role.Name] = r
		}
		return nil
	}); err != nil {
		// The file is written and is what the shift reads; only the receipt
		// stamp is lost.
		log.Printf("handoff.put: %s: record receipt: %v", sess.Key(), err)
	}
	events.Emit(c.Events, events.Event{
		Kind:       events.KindHandoffPut,
		Severity:   events.SeverityInfo,
		Workspace:  t.Workspace,
		Team:       t.Name,
		Role:       role.Name,
		Session:    sess.Key(),
		Generation: t.Generation,
		Message:    fmt.Sprintf("session %s handed off through the daemon: %d bytes to %s, requested %s", sess.Key(), len(body), path, req.RequestedAt.Format("2006-01-02T15:04:05Z07:00")),
	})
	return sess, HandoffPut{Path: path, Bytes: len(body)}, nil
}

// handoffBody is the text, trimmed of trailing space, ending with the marker as
// its last line and a newline. A text that already ends with the marker does
// not get it twice.
func handoffBody(text, marker string) string {
	body := strings.TrimRight(text, " \t\r\n")
	if strings.TrimSpace(body[strings.LastIndexByte(body, '\n')+1:]) != marker {
		body += "\n" + marker
	}
	return body + "\n"
}

// writeHandoffFile writes body at path, a file directly in the directory marvel
// recorded in req, without following a link anywhere. It opens the directory as
// an os.Root and compares the opened handle's identity with the recorded one,
// writes a temporary file there with O_EXCL and O_NOFOLLOW, checks the target
// is absent or a regular file, and renames the temporary file over it. A
// refusal leaves nothing behind.
func writeHandoffFile(path string, req api.ShiftRequest, body string) error {
	dir, base := filepath.Split(path)
	dir = filepath.Clean(dir)
	if base == "" || base == "." || base == ".." || strings.ContainsRune(base, filepath.Separator) {
		return fmt.Errorf("%w: %q is not a file name", ErrHandoffDir, base)
	}
	if req.DirIno == 0 {
		return fmt.Errorf("%w: no directory identity was recorded when the handoff was requested", ErrHandoffDir)
	}
	// The directory and every ancestor must be real directories, not links. The
	// Lstat refuses a link in the last element; EvalSymlinks refuses one
	// anywhere above it.
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrHandoffDir, dir)
	}
	if resolved, err := filepath.EvalSymlinks(dir); err != nil || resolved != dir {
		return fmt.Errorf("%w: %s resolves through a link", ErrHandoffDir, dir)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("%w: open %s: %v", ErrHandoffDir, dir, err)
	}
	defer func() { _ = root.Close() }()

	// The identity is read from the opened handle, so a swap of the directory
	// or an ancestor after the request is caught here, and a swap after this
	// check cannot redirect the write: root holds the directory it opened.
	dh, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("%w: open %s: %v", ErrHandoffDir, dir, err)
	}
	info, err := dh.Stat()
	_ = dh.Close()
	if err != nil {
		return fmt.Errorf("%w: stat %s: %v", ErrHandoffDir, dir, err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || uint64(st.Dev) != req.DirDev || uint64(st.Ino) != req.DirIno { //nolint:unconvert // Dev is int32 on darwin
		return fmt.Errorf("%w: %s is not the directory marvel recorded when it asked", ErrHandoffDir, dir)
	}

	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return fmt.Errorf("handoff.put: temporary name: %w", err)
	}
	tmp := ".marvel-handoff-" + hex.EncodeToString(rnd[:]) + ".tmp"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("handoff.put: create temporary file in %s: %w", dir, err)
	}
	cleanup := func() { _ = root.Remove(tmp) }
	if _, err := f.WriteString(body); err != nil {
		_ = f.Close()
		cleanup()
		return fmt.Errorf("handoff.put: write: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		cleanup()
		return fmt.Errorf("handoff.put: sync: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return fmt.Errorf("handoff.put: close: %w", err)
	}

	switch target, err := root.Lstat(base); {
	case err == nil && !target.Mode().IsRegular():
		cleanup()
		return fmt.Errorf("%w: %s exists and is not a regular file", ErrHandoffDir, path)
	case err != nil && !errors.Is(err, os.ErrNotExist):
		cleanup()
		return fmt.Errorf("handoff.put: inspect %s: %w", path, err)
	}
	if err := root.Rename(tmp, base); err != nil {
		cleanup()
		return fmt.Errorf("handoff.put: place %s: %w", path, err)
	}
	return nil
}

// emitPutRefused names a refused put. The session is empty when the token
// identified none; neither the token nor the text is ever in the message.
func (c *Controller) emitPutRefused(sess api.Session, err error) {
	events.Emit(c.Events, events.Event{
		Kind:      events.KindHandoffPutRefused,
		Severity:  events.SeverityWarning,
		Workspace: sess.Workspace,
		Team:      sess.Team,
		Role:      sess.Role,
		Session:   sessionKeyOrEmpty(sess),
		Message:   err.Error(),
	})
}

func sessionKeyOrEmpty(sess api.Session) string {
	if sess.Name == "" {
		return ""
	}
	return sess.Key()
}
