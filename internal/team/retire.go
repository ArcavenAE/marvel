package team

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/arcavenae/marvel/contracts/go/envelope"
	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// Retirement: a seat being removed is asked for its handoff first, given a
// bounded window, and its pane is killed when the handoff is written or the
// window ends (docs/design/work-prune.md section 6). It covers scale-down, a
// role dropped from the manifest, and a prune.

// retireNotice opens the notice a retiring seat is sent. Everything from
// "write your handoff now" on is built by prepareHandoffNotice, as the shift
// notices are, so a seat that recognises the shift ask recognises this one.
const retireNotice = "retire: this seat is being removed; write your handoff now"

// Outcomes of a retirement, as session.retired reports them (section 6.3).
// There is no run-finished or run-cancelled: headless seats are left alone by
// operator ruling 2026-10-28, so none is retired.
const (
	retireHandoffWritten = "handoff-written"
	retireHandoffMissing = "handoff-missing"
	retireNotAsked       = "not-asked"
)

// retiredScreenLines is how much of a pane's history is saved before the kill.
const retiredScreenLines = 500

// errHeadlessNotBuilt is retireHeadless's answer: a running headless seat is
// left alone by operator ruling 2026-10-28, so it is never retired.
var errHeadlessNotBuilt = errors.New("a running headless seat is left alone by operator ruling 2026-10-28")

// retireHeadless is where a running headless seat would be retired. Headless
// seats are left alone by operator ruling 2026-10-28 (section 6.2), so it
// returns errHeadlessNotBuilt and the caller keeps the delete it always made.
// The prune defers a team with a live headless seat (pruneVerdict), so no
// prune reaches here.
func (c *Controller) retireHeadless(*api.Team, *api.Role, api.Session) error {
	return errHeadlessNotBuilt
}

// isRetiring reports whether the session at key is being retired.
func isRetiring(t *api.Team, key string) bool {
	_, ok := t.Retiring[key]
	return ok
}

// withoutRetiring drops the sessions being retired, which no longer count as
// actual for their role (section 6.1).
func withoutRetiring(t *api.Team, sessions []api.Session) []api.Session {
	if len(t.Retiring) == 0 {
		return sessions
	}
	out := sessions[:0:0]
	for _, s := range sessions {
		if !isRetiring(t, s.Key()) {
			out = append(out, s)
		}
	}
	return out
}

// retireSession removes sess with a handoff ask, in place of a plain delete.
// role is nil for a role the manifest dropped: its handoff declaration went
// with the apply, so the seat gets the plain grace. A seat that is not running
// is deleted at once, not asked. Caller holds c.mu.
func (c *Controller) retireSession(t *api.Team, role *api.Role, sess api.Session, now time.Time) {
	if isRetiring(t, sess.Key()) {
		return
	}
	if sess.State != api.SessionRunning {
		reason := "not running"
		if sess.State == api.SessionPending {
			reason = "pending"
		}
		c.emitRetired(t, sess, retireNotAsked, reason, "")
		if err := c.sessMgr.Delete(sess.Key()); err != nil {
			log.Printf("retire: delete session %s: %v", sess.Key(), err)
		}
		return
	}
	if role != nil && role.Runtime.Mode == api.RuntimeModeHeadless {
		if err := c.retireHeadless(t, role, sess); errors.Is(err, errHeadlessNotBuilt) {
			if err := c.sessMgr.Delete(sess.Key()); err != nil {
				log.Printf("retire: delete headless session %s: %v", sess.Key(), err)
			}
		}
		return
	}
	// One slot per seat: a pending shift request on this seat ends here.
	if req, ok := t.ShiftRequests[sess.Role]; ok && req.Session == sess.Key() {
		c.dropShiftRequest(t, sess.Role)
	}

	template, marker, window := "", "", api.DefaultShiftHandoffWindow
	if role != nil && role.Shift != nil {
		template, marker, window = role.Shift.Handoff, role.Shift.HandoffMarker, role.Shift.HandoffWindowOrDefault()
	}
	prep := prepareHandoffNotice(retireNotice, template, marker, window, sess)
	if prep.path == "" {
		prep.text += fmt.Sprintf("; you have %s", window)
	}
	undelivered := ""
	if c.Notify == nil {
		undelivered = "no notifier"
	} else if err := c.Notify(sess, prep.text, NoticeRetire); err != nil {
		undelivered = err.Error()
	}
	req := api.RetireRequest{
		Session: sess.Key(), Role: sess.Role, RequestedAt: now, Window: window,
		Path: prep.path, Marker: marker, NoticeUndelivered: undelivered,
		DirError: prep.dirError, DirDev: prep.dirDev, DirIno: prep.dirIno,
	}
	if prep.path == "" {
		req.Marker = ""
	}
	if err := c.store.UpdateTeam(t.Key(), func(live *api.Team) error {
		if live.Retiring == nil {
			live.Retiring = make(map[string]api.RetireRequest)
		}
		live.Retiring[sess.Key()] = req
		return nil
	}); err != nil {
		log.Printf("retire: %s: record request for %s: %v", t.Key(), sess.Key(), err)
		if prep.heldDir != nil {
			_ = prep.heldDir.Close()
		}
		return
	}
	if t.Retiring == nil {
		t.Retiring = make(map[string]api.RetireRequest)
	}
	t.Retiring[sess.Key()] = req
	c.holdRetireDir(req, prep.heldDir)
	if c.retireSeen == nil {
		c.retireSeen = make(map[string]bool)
	}
	c.retireSeen[sess.Key()] = true
}

// advanceRetirements moves each of the team's retirements on: a seat that
// wrote its handoff is killed, one past its window is killed, one that ended
// on its own is recorded, and a request whose seat was replaced is dropped. It
// runs at the start of every reconcile of the team, a shift in progress
// included. Caller holds c.mu.
func (c *Controller) advanceRetirements(t *api.Team, now time.Time) {
	if len(t.Retiring) == 0 {
		return
	}
	keys := make([]string, 0, len(t.Retiring))
	for k := range t.Retiring {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		c.advanceRetirement(t, t.Retiring[key], now)
	}
}

func (c *Controller) advanceRetirement(t *api.Team, req api.RetireRequest, now time.Time) {
	sess, err := c.store.GetSession(req.Session)
	if err != nil || !sess.CreatedAt.Before(req.RequestedAt) {
		// The seat is gone, or a session created at or after the ask holds
		// its key: that one was never asked.
		c.dropRetirement(t, req)
		return
	}
	seen := c.retireSeen[req.Session]
	if c.retireSeen == nil {
		c.retireSeen = make(map[string]bool)
	}
	c.retireSeen[req.Session] = true

	var read handoffProbeResult
	var haveRead bool
	if req.Path != "" {
		read, haveRead = c.handoffProbes.complete(req.Session, req.Path, req.Marker, now)
	}
	if !sess.State.CountsAsAlive() {
		switch {
		case !seen:
			// First look since this controller started: the pane did not
			// survive the restart.
			c.emitRetired(t, sess, retireNotAsked, "pane gone", "")
		case !c.retireEndSettled(req, read, haveRead, now):
			// A seat told to leave often writes its handoff and exits at
			// once; wait for a read that cannot have missed the file.
			return
		case haveRead && read.complete:
			c.emitRetired(t, sess, retireHandoffWritten, "the seat ended after writing its handoff", "")
		default:
			c.emitRetired(t, sess, retireHandoffMissing, "seat ended"+c.deliverRetireMissing(t, sess, req, "seat ended", now), "")
		}
		if err := c.sessMgr.Delete(sess.Key()); err != nil {
			log.Printf("retire: delete ended session %s: %v", sess.Key(), err)
		}
		c.dropRetirement(t, req)
		return
	}
	if haveRead && read.complete {
		c.killRetired(t, sess, req, retireHandoffWritten, fmt.Sprintf("%s ends in the marker", req.Path))
		return
	}
	deadline := req.RequestedAt.Add(req.Window)
	if now.Before(deadline) {
		return
	}
	// A read that began before the deadline cannot show the marker is absent:
	// the seat may have written in time. Kill only on a read begun after it.
	if req.Path != "" && (!haveRead || read.startedAt.Before(deadline)) {
		return
	}
	reason := retireMissingReason(req)
	c.killRetired(t, sess, req, retireHandoffMissing, reason+c.deliverRetireMissing(t, sess, req, reason, now))
}

// retireEndSettled reports whether the outcome of a retiring seat that is no
// longer alive can be decided now. It notes when the seat was first seen
// ended, and settles on a finished marker read begun at or after that moment,
// since an earlier read may have begun before the seat wrote its file. At the
// window it settles on the newest read there is. A retirement with no handoff
// path has no file to wait for and settles at once (the step 4 ruling on a
// seat that exits, 2026-10-28). Caller holds c.mu.
func (c *Controller) retireEndSettled(req api.RetireRequest, read handoffProbeResult, haveRead bool, now time.Time) bool {
	if req.Path == "" {
		return true
	}
	if c.retireEnded == nil {
		c.retireEnded = make(map[string]time.Time)
	}
	endedAt, ok := c.retireEnded[req.Session]
	if !ok {
		endedAt = now
		c.retireEnded[req.Session] = endedAt
	}
	if haveRead && !read.startedAt.Before(endedAt) {
		return true
	}
	return !now.Before(req.RequestedAt.Add(req.Window))
}

// retireMissingReason says why no handoff was observed by the end of the
// window.
func retireMissingReason(req api.RetireRequest) string {
	reason := fmt.Sprintf("window %s passed", req.Window)
	if req.Path == "" {
		reason += "; no handoff path is declared, so the window was a plain grace"
	} else {
		reason += fmt.Sprintf("; no regular file at %s ends in the marker", req.Path)
	}
	if req.NoticeUndelivered != "" {
		reason += fmt.Sprintf("; the notice was never delivered (%s)", req.NoticeUndelivered)
	}
	if req.DirError != "" {
		reason += "; " + req.DirError
	}
	return reason
}

// killRetired saves the pane's screen, records the outcome and kills the pane,
// in that order (the step 4 ruling, Q2). A screen that cannot be saved is
// named in the event and does not hold the kill back.
func (c *Controller) killRetired(t *api.Team, sess api.Session, req api.RetireRequest, outcome, reason string) {
	saved := c.saveRetiredScreen(sess)
	c.emitRetired(t, sess, outcome, reason, saved)
	if err := c.sessMgr.Delete(sess.Key()); err != nil {
		log.Printf("retire: delete session %s: %v", sess.Key(), err)
	}
	c.dropRetirement(t, req)
}

// saveRetiredScreen writes the last retiredScreenLines lines of the pane's
// history to <marvel home>/retired/<workspace>/<name>-<stamp>.txt, never over
// an existing file, and says what it did.
func (c *Controller) saveRetiredScreen(sess api.Session) string {
	if c.CaptureScreen == nil || c.RetiredDir == "" {
		return "screen not saved: no capture configured"
	}
	text, err := c.CaptureScreen(sess)
	if err != nil {
		return fmt.Sprintf("screen not saved: %v", err)
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > retiredScreenLines {
		lines = lines[len(lines)-retiredScreenLines:]
	}
	dir := filepath.Join(c.RetiredDir, sess.Workspace)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Sprintf("screen not saved: %v", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.txt", sess.Name, c.nowUTC().Format("20060102T150405Z")))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Sprintf("screen not saved: %v", err)
	}
	_, werr := f.WriteString(strings.Join(lines, "\n") + "\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return fmt.Sprintf("screen not saved: %v", werr)
	}
	return "screen saved to " + path
}

func (c *Controller) emitRetired(t *api.Team, sess api.Session, outcome, reason, saved string) {
	sev := events.SeverityInfo
	if outcome == retireHandoffMissing {
		sev = events.SeverityWarning
	}
	msg := fmt.Sprintf("outcome=%s: %s", outcome, reason)
	if saved != "" {
		msg += "; " + saved
	}
	events.Emit(c.Events, events.Event{
		Kind:       events.KindSessionRetired,
		Severity:   sev,
		Workspace:  t.Workspace,
		Team:       t.Name,
		Role:       sess.Role,
		Session:    sess.Key(),
		Generation: sess.Generation,
		Message:    msg,
	})
}

// deliverRetireMissing tells the team's supervisor role that a retiring seat
// left with no handoff, by the bus the missing-handoff delivery uses
// (docs/design/handoff-missing-delivery.md), once and never by typing into a
// pane. It is skipped when the retiring seat is itself a supervisor, as the
// shift's delivery skips it. It returns the clause the event carries.
func (c *Controller) deliverRetireMissing(t *api.Team, sess api.Session, req api.RetireRequest, reason string, now time.Time) string {
	switch {
	case c.Publisher == nil:
		return "; published: none, no managed bus"
	case sess.Role == supervisorRoleName:
		return "; published: none, the retiring seat is itself a supervisor"
	case !addressable.MatchString(t.Workspace) || !addressable.MatchString(t.Name):
		return "; published: none, team name not addressable"
	}
	corr := fmt.Sprintf("retire-missing/%s/%s/%s/%d", t.Workspace, t.Name, sess.Name, req.RequestedAt.Unix())
	text := fmt.Sprintf("retire: seat %s in %s role %s was removed with no handoff observed (%s), asked %s; its last screen is saved under the retired directory of the marvel home.",
		sess.Key(), t.Key(), sess.Role, reason, req.RequestedAt.UTC().Format(time.RFC3339))
	env := envelope.Envelope{
		SchemaVersion: 1,
		MessageID:     newULID(now),
		CorrelationID: &corr,
		Sender:        envelope.Sender{AgentID: escalationSender, Workspace: t.Workspace},
		Recipient:     envelope.Recipient{Address: fmt.Sprintf("role://%s/%s", t.Name, supervisorRoleName)},
		Performative:  envelope.PerformativeINFORM,
		Authority:     &envelope.Authority{Strength: envelope.StrengthNone},
		Content:       envelope.Content{Type: envelope.TypeSignal, Data: text},
		SentAt:        now.UTC(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), publishTimeout)
	defer cancel()
	if err := c.Publisher.PublishRoleInbox(ctx, t.Workspace, t.Name, supervisorRoleName, env, corr); err != nil {
		return fmt.Sprintf("; published: failed (%v)", err)
	}
	return "; published: sent"
}

// dropRetirement ends a retirement's record, and finishes the removal of a
// dropped role whose last seat this was.
func (c *Controller) dropRetirement(t *api.Team, req api.RetireRequest) {
	c.handoffProbes.forget(req.Session)
	delete(c.retireSeen, req.Session)
	delete(c.retireEnded, req.Session)
	c.releaseRetireDir(req.Session)
	if err := c.store.UpdateTeam(t.Key(), func(live *api.Team) error {
		delete(live.Retiring, req.Session)
		return nil
	}); err != nil && !errors.Is(err, api.ErrNotFound) {
		log.Printf("retire: %s: drop request for %s: %v", t.Key(), req.Session, err)
		return
	}
	delete(t.Retiring, req.Session)
	c.finishRoleRemoval(t, req.Role)
}

// roleOf is the team's role named name, or nil.
func roleOf(t *api.Team, name string) *api.Role {
	for i := range t.Roles {
		if t.Roles[i].Name == name {
			return &t.Roles[i]
		}
	}
	return nil
}

// roleRemains reports whether a role still holds a seat on the team: a
// retiring one, or any manifest session.
func (c *Controller) roleRemains(t *api.Team, role string) bool {
	for _, r := range t.Retiring {
		if r.Role == role {
			return true
		}
	}
	for _, s := range c.store.ListSessionsByTeamRole(t.Workspace, t.Name, role) {
		if s.Generation != 0 {
			return true
		}
	}
	return false
}

// finishRoleRemoval forgets a dropped role's health, admission and bus holds
// and records role.removed, once the role's last seat has gone (the step 4
// ruling, Q5). Until then a retiring seat still holds the role. It decides
// from what the store holds (the role absent from the team, no session of it
// left, no retirement for it), so a restart inside the grace still finishes
// the role.
func (c *Controller) finishRoleRemoval(t *api.Team, role string) {
	if roleOf(t, role) != nil || c.roleRemains(t, role) {
		return
	}
	roleKey := t.Workspace + "/" + t.Name + "/" + role
	msg := "role removed from manifest; its last seat has gone"
	if n, ok := c.roleDrained[roleKey]; ok {
		msg = fmt.Sprintf("role removed from manifest, drained %d session(s)", n)
	}
	delete(c.roleDrained, roleKey)
	delete(c.roleHealth, roleKey)
	c.forgetRoleHealth(roleKey)
	delete(c.admissionHolds, roleKey)
	delete(c.busHolds, roleKey)
	log.Printf("reconcile: role %s of team %s: %s", role, t.Key(), msg)
	events.Emit(c.Events, events.Event{
		Kind:      events.KindRoleRemoved,
		Severity:  events.SeverityWarning,
		Workspace: t.Workspace,
		Team:      t.Name,
		Role:      role,
		Message:   msg,
	})
}

// holdRetireDir keeps f, the directory opened for req, until the retirement
// ends, as adoptHandoffDir does for a shift request.
func (c *Controller) holdRetireDir(req api.RetireRequest, f *os.File) {
	c.releaseRetireDir(req.Session)
	if f == nil {
		return
	}
	if c.retireDirs == nil {
		c.retireDirs = make(map[string]heldDir)
	}
	c.retireDirs[req.Session] = heldDir{session: req.Session, requestedAt: req.RequestedAt, f: f}
}

func (c *Controller) releaseRetireDir(session string) {
	if h, ok := c.retireDirs[session]; ok {
		_ = h.f.Close()
		delete(c.retireDirs, session)
	}
}

// heldRetireDirInfoLocked returns the identity of the directory held for req,
// or false when none is held for this request. The caller holds c.mu.
func (c *Controller) heldRetireDirInfoLocked(req api.RetireRequest) (os.FileInfo, bool) {
	h, ok := c.retireDirs[req.Session]
	if !ok || !h.requestedAt.Equal(req.RequestedAt) {
		return nil, false
	}
	info, err := h.f.Stat()
	if err != nil {
		return nil, false
	}
	return info, true
}

// resumeRetireDirs re-holds the handoff directory of each retirement restored
// from the store, when it still has the recorded identity, as a shift request
// is re-adopted after a restart. Caller holds c.mu.
func (c *Controller) resumeRetireDirs(t *api.Team) {
	for _, req := range t.Retiring {
		if _, held := c.retireDirs[req.Session]; held || req.Path == "" {
			continue
		}
		dir := filepath.Dir(req.Path)
		dev, ino, err := dirIdentity(dir)
		if err != nil || dev != req.DirDev || ino != req.DirIno {
			continue
		}
		if f, err := os.Open(dir); err == nil {
			c.holdRetireDir(req, f)
		}
	}
}
