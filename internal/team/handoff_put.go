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
//  1. The token is bound and matches a session that is running, not ending,
//     and was created before the request it holds (the rule the shift path and
//     the store's write-authority lookup already use). The request names no
//     session.
//  2. That session holds the role's pending request (one per role), not
//     escalated.
//  3. The path comes from the role's declared template and the stored session,
//     never from the request.
//  4. No link is followed in the file or its directory, and the directory is
//     the one recorded when marvel asked: checked on the opened handle, so a
//     swap after the check cannot redirect the write.
//
// The file is written off the controller lock (marvel#444: a slow filesystem
// must not stall every team). The request is checked under the lock before the
// write and again after it; a request that was dropped, replaced or escalated
// in between has its file removed again and the put is refused. Same-uid seats
// are not isolated from each other by marvel; the residuals are stated in the
// design (section 2). The text is capped, never truncated.
func (c *Controller) PutHandoff(token, text string) (HandoffPut, error) {
	c.mu.Lock()
	plan, err := c.planHandoffPutLocked(token, text)
	c.mu.Unlock()
	if err != nil {
		c.emitPutRefused(plan.sess, err)
		return HandoffPut{}, err
	}

	ident, err := writeHandoffFile(plan.path, plan.req, plan.body, plan.held)
	if err != nil {
		c.emitPutRefused(plan.sess, err)
		return HandoffPut{}, err
	}
	// The put's file stays open until the decision below, so its inode cannot
	// be freed and reused while removeHandoffFile compares against it.
	defer ident.close()
	if c.afterHandoffWrite != nil {
		c.afterHandoffWrite()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.recheckHandoffPutLocked(token, plan); err != nil {
		removeHandoffFile(plan.path, ident)
		c.emitPutRefused(plan.sess, err)
		return HandoffPut{}, err
	}
	now := c.nowUTC()
	if err := c.store.UpdateTeam(plan.teamKey, func(live *api.Team) error {
		if r, ok := live.ShiftRequests[plan.role]; ok && r.Session == plan.sess.Key() {
			r.PutAt = now
			live.ShiftRequests[plan.role] = r
		}
		return nil
	}); err != nil {
		// The file is written and is what the shift reads; only the receipt
		// stamp is lost.
		log.Printf("handoff.put: %s: record receipt: %v", plan.sess.Key(), err)
	}
	events.Emit(c.Events, events.Event{
		Kind:       events.KindHandoffPut,
		Severity:   events.SeverityInfo,
		Workspace:  plan.sess.Workspace,
		Team:       plan.sess.Team,
		Role:       plan.role,
		Session:    plan.sess.Key(),
		Generation: plan.generation,
		Message:    fmt.Sprintf("session %s handed off through the daemon: %d bytes to %s, requested %s", plan.sess.Key(), len(plan.body), plan.path, plan.req.RequestedAt.Format("2006-01-02T15:04:05Z07:00")),
	})
	return HandoffPut{Path: plan.path, Bytes: len(plan.body)}, nil
}

// handoffPutPlan is what the checks under the lock decide: whose handoff, for
// which request, where it goes and what it holds.
type handoffPutPlan struct {
	sess       api.Session
	teamKey    string
	role       string
	generation int64
	req        api.ShiftRequest
	path, body string
	// held is the identity of the directory held open since the request, nil
	// for a request restored after a restart.
	held os.FileInfo
}

// liveSessionForPut is the gate's liveness rule: the session the token was
// minted for is running, not ending, and was created before the request, so a
// health restart that replaced the seat asked (same key, new token) cannot
// write the handoff of the one that was. It is the rule the store applies to
// write authority (AuthenticateLiveSession) and shift_handoff.go applies to a
// pending request.
func (c *Controller) liveSessionForPut(token string, req *api.ShiftRequest) (api.Session, error) {
	sess, err := c.store.SessionByHeartbeatToken(token)
	if err != nil {
		return api.Session{}, ErrHandoffUnauthorized
	}
	live, err := c.store.AuthenticateLiveSession(sess.Key(), token)
	if err != nil {
		return sess, fmt.Errorf("%w: session %s is %s and not running", ErrHandoffNotRequested, sess.Key(), sess.State)
	}
	if req != nil && !live.CreatedAt.Before(req.RequestedAt) {
		return live, fmt.Errorf("%w: session %s was created at or after the request, so it is not the seat asked", ErrHandoffNotRequested, live.Key())
	}
	return live, nil
}

func (c *Controller) planHandoffPutLocked(token, text string) (handoffPutPlan, error) {
	sess, err := c.liveSessionForPut(token, nil)
	if err != nil {
		return handoffPutPlan{sess: sess}, err
	}
	plan := handoffPutPlan{sess: sess}
	if strings.TrimSpace(text) == "" || len(text) > MaxHandoffPutBytes {
		return plan, fmt.Errorf("%w: %d bytes, limit %d", ErrHandoffTooLarge, len(text), MaxHandoffPutBytes)
	}

	teamKey := sess.Workspace + "/" + sess.Team
	t, err := c.store.GetTeam(teamKey)
	if err != nil {
		return plan, fmt.Errorf("%w: team %s: %v", ErrHandoffNotRequested, teamKey, err)
	}
	var role *api.Role
	for i := range t.Roles {
		if t.Roles[i].Name == sess.Role {
			role = &t.Roles[i]
			break
		}
	}
	if role == nil {
		return plan, fmt.Errorf("%w: role %s is not in team %s", ErrHandoffNotRequested, sess.Role, teamKey)
	}
	req, ok := t.ShiftRequests[role.Name]
	if !ok || req.Session != sess.Key() || req.Escalated {
		return plan, fmt.Errorf("%w: role %s", ErrHandoffNotRequested, role.Name)
	}
	if _, err := c.liveSessionForPut(token, &req); err != nil {
		return plan, err
	}
	if role.Shift == nil || role.Shift.Handoff == "" || role.Shift.HandoffMarker == "" {
		return plan, fmt.Errorf("%w: role %s", ErrHandoffNoPath, role.Name)
	}
	path, err := handoffPath(role.Shift.Handoff, sess)
	if err != nil {
		return plan, fmt.Errorf("%w: %v", ErrHandoffNoPath, err)
	}
	plan.teamKey, plan.role, plan.generation, plan.req = teamKey, role.Name, t.Generation, req
	plan.path, plan.body = path, handoffBody(text, role.Shift.HandoffMarker)
	if info, ok := c.heldHandoffDirInfoLocked(teamKey, role.Name, req); ok {
		plan.held = info
	}
	return plan, nil
}

// recheckHandoffPutLocked confirms, after the write, that the request the file
// was written for still stands: the same request is pending and not escalated,
// and the same session still holds it and is live.
func (c *Controller) recheckHandoffPutLocked(token string, plan handoffPutPlan) error {
	t, err := c.store.GetTeam(plan.teamKey)
	if err != nil {
		return fmt.Errorf("%w: team %s: %v", ErrHandoffNotRequested, plan.teamKey, err)
	}
	req, ok := t.ShiftRequests[plan.role]
	if !ok || req.Session != plan.sess.Key() || !req.RequestedAt.Equal(plan.req.RequestedAt) || req.Escalated {
		return fmt.Errorf("%w: role %s changed while the handoff was written", ErrHandoffNotRequested, plan.role)
	}
	if _, err := c.liveSessionForPut(token, &req); err != nil {
		return err
	}
	return nil
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
func writeHandoffFile(path string, req api.ShiftRequest, body string, held os.FileInfo) (writtenFile, error) {
	dir, base := filepath.Split(path)
	dir = filepath.Clean(dir)
	if base == "" || base == "." || base == ".." || strings.ContainsRune(base, filepath.Separator) {
		return writtenFile{}, fmt.Errorf("%w: %q is not a file name", ErrHandoffDir, base)
	}
	if req.DirIno == 0 {
		return writtenFile{}, fmt.Errorf("%w: no directory identity was recorded when the handoff was requested", ErrHandoffDir)
	}
	// The directory and every ancestor must be real directories, not links. The
	// Lstat refuses a link in the last element; EvalSymlinks refuses one
	// anywhere above it.
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() {
		return writtenFile{}, fmt.Errorf("%w: %s is not a directory", ErrHandoffDir, dir)
	}
	if resolved, err := filepath.EvalSymlinks(dir); err != nil || resolved != dir {
		return writtenFile{}, fmt.Errorf("%w: %s resolves through a link", ErrHandoffDir, dir)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return writtenFile{}, fmt.Errorf("%w: open %s: %v", ErrHandoffDir, dir, err)
	}
	defer func() { _ = root.Close() }()

	// The identity is read from the opened handle, so a swap of the directory
	// or an ancestor after the request is caught here, and a swap after this
	// check cannot redirect the write: root holds the directory it opened.
	dh, err := root.Open(".")
	if err != nil {
		return writtenFile{}, fmt.Errorf("%w: open %s: %v", ErrHandoffDir, dir, err)
	}
	info, err := dh.Stat()
	_ = dh.Close()
	if err != nil {
		return writtenFile{}, fmt.Errorf("%w: stat %s: %v", ErrHandoffDir, dir, err)
	}
	if held != nil {
		// The directory marvel opened when it asked is still open, so its inode
		// cannot have been freed and given to another directory. Comparing the
		// opened handle with it needs no number to stay unique.
		if !os.SameFile(held, info) {
			return writtenFile{}, fmt.Errorf("%w: %s is not the directory marvel held when it asked", ErrHandoffDir, dir)
		}
	} else if st, ok := info.Sys().(*syscall.Stat_t); !ok || uint64(st.Dev) != req.DirDev || uint64(st.Ino) != req.DirIno { //nolint:unconvert // Dev is int32 on darwin
		// A request restored after a daemon restart holds nothing open, so it
		// falls back to the numbers recorded when marvel asked. They can be
		// reused by another directory in the gap, which is the window the held
		// descriptor closes for every request made since the restart.
		return writtenFile{}, fmt.Errorf("%w: %s is not the directory marvel recorded when it asked", ErrHandoffDir, dir)
	}

	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return writtenFile{}, fmt.Errorf("handoff.put: temporary name: %w", err)
	}
	tmp := ".marvel-handoff-" + hex.EncodeToString(rnd[:]) + ".tmp"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return writtenFile{}, fmt.Errorf("handoff.put: create temporary file in %s: %w", dir, err)
	}
	cleanup := func() { _ = root.Remove(tmp) }
	if _, err := f.WriteString(body); err != nil {
		_ = f.Close()
		cleanup()
		return writtenFile{}, fmt.Errorf("handoff.put: write: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		cleanup()
		return writtenFile{}, fmt.Errorf("handoff.put: sync: %w", err)
	}

	switch target, err := root.Lstat(base); {
	case err == nil && !target.Mode().IsRegular():
		_ = f.Close()
		cleanup()
		return writtenFile{}, fmt.Errorf("%w: %s exists and is not a regular file", ErrHandoffDir, path)
	case err != nil && !errors.Is(err, os.ErrNotExist):
		_ = f.Close()
		cleanup()
		return writtenFile{}, fmt.Errorf("handoff.put: inspect %s: %w", path, err)
	}
	if err := root.Rename(tmp, base); err != nil {
		_ = f.Close()
		cleanup()
		return writtenFile{}, fmt.Errorf("handoff.put: place %s: %w", path, err)
	}
	return writtenFile{f: f}, nil
}

// writtenFile is the file a put placed, held open from its creation (the
// rename keeps the inode). While it is open the inode cannot be freed, so no
// later file can take its number: a refused put compares the path against this
// handle, and a device and inode read earlier could not promise that. The zero
// value holds nothing and identifies nothing.
type writtenFile struct{ f *os.File }

func (w writtenFile) close() {
	if w.f != nil {
		_ = w.f.Close()
	}
}

// removeHandoffFile removes the file at path if it is the one the put placed
// and held. With no held file it removes nothing. Best effort: a failure is
// logged, and a file that changed is left.
func removeHandoffFile(path string, held writtenFile) {
	if held.f == nil {
		return
	}
	heldInfo, err := held.f.Stat()
	if err != nil {
		return
	}
	dir, base := filepath.Split(path)
	root, err := os.OpenRoot(filepath.Clean(dir))
	if err != nil {
		log.Printf("handoff.put: remove %s after a refused put: %v", path, err)
		return
	}
	defer func() { _ = root.Close() }()
	fi, err := root.Lstat(base)
	if err != nil || !fi.Mode().IsRegular() || !os.SameFile(fi, heldInfo) {
		return
	}
	if err := root.Remove(base); err != nil {
		log.Printf("handoff.put: remove %s after a refused put: %v", path, err)
	}
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
