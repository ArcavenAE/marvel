package team

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// The max-age trigger and its handoff (marvel#437, docs/design/shift-trigger-list.md
// D3 to D5). Max age never shifts a seat by itself. It asks the seat for a
// handoff, watches one declared file for a terminal marker, and shifts only
// on the marker. With no marker by the end of the window, or no file to
// watch, it escalates to the team's supervisor and leaves the seat running:
// the written succession contract forbids an automatic shift with no handoff
// and no watcher.

// handoffNotice is what a seat past its max age is sent.
const handoffNotice = "shift: max age reached; write your handoff now"

// maxHandoffTail bounds how much of a handoff file marvel reads to find its
// last line.
const maxHandoffTail = 4096

// Stages of the max-age condition (design D4).
const (
	ageStageQuiet = "quiet"
	ageStageHard  = "hard"
)

// firstSessionOverAge returns the first running current-generation session of
// role that is past cond.MaxAge and either quiet for cond.QuietFor (the quiet
// stage) or past MaxAge+MaxDefer whatever its activity (the hard stage). Age
// runs from CreatedAt, so it survives a daemon restart and starts over on a
// health restart, which creates a new session (D3). Activity is the context
// feed timestamp the activity advisory reads, or spawn when there is none.
// Only running sessions count, so a finished headless run never ages out (D7).
func (c *Controller) firstSessionOverAge(t *api.Team, role *api.Role, cond api.ShiftCondition, now time.Time) (sess api.Session, age time.Duration, stage string, ok bool) {
	quietFor := cond.QuietFor
	if quietFor <= 0 {
		quietFor = api.DefaultShiftQuietFor
	}
	maxDefer := cond.MaxDefer
	if maxDefer <= 0 {
		maxDefer = api.DefaultShiftMaxDefer
	}
	for _, s := range c.store.ListSessionsByTeamRoleGeneration(t.Workspace, t.Name, role.Name, t.Generation) {
		if s.State != api.SessionRunning || s.CreatedAt.IsZero() {
			continue
		}
		age := now.Sub(s.CreatedAt)
		if age < cond.MaxAge {
			continue
		}
		lastActive := s.CreatedAt
		if s.ContextAt.After(lastActive) {
			lastActive = s.ContextAt
		}
		switch {
		case now.Sub(lastActive) >= quietFor:
			return s, age, ageStageQuiet, true
		case age >= cond.MaxAge+maxDefer:
			return s, age, ageStageHard, true
		}
	}
	return api.Session{}, 0, "", false
}

// requestHandoff asks sess for its handoff and records the pending request on
// the team, durably, so a daemon restart inside the window resumes it.
func (c *Controller) requestHandoff(t *api.Team, role *api.Role, cond api.ShiftCondition, sess api.Session, age time.Duration, stage string, now time.Time) {
	text := handoffNotice
	if role.Shift.Handoff != "" {
		text += fmt.Sprintf(" to %s, ending with the line %q", handoffPath(role.Shift.Handoff, sess), role.Shift.HandoffMarker)
	}
	delivery := "delivered"
	if c.Notify == nil {
		delivery = "not delivered (no notifier)"
	} else if err := c.Notify(sess, text); err != nil {
		delivery = fmt.Sprintf("not delivered (%v)", err)
	}

	req := api.ShiftRequest{Session: sess.Key(), Cause: cond.On, RequestedAt: now}
	if err := c.store.UpdateTeam(t.Key(), func(live *api.Team) error {
		if live.ShiftRequests == nil {
			live.ShiftRequests = make(map[string]api.ShiftRequest)
		}
		live.ShiftRequests[role.Name] = req
		return nil
	}); err != nil {
		log.Printf("shift: %s role %s: record handoff request: %v", t.Key(), role.Name, err)
		return
	}
	if t.ShiftRequests == nil {
		t.ShiftRequests = make(map[string]api.ShiftRequest)
	}
	t.ShiftRequests[role.Name] = req

	events.Emit(c.Events, events.Event{
		Kind:       events.KindShiftHandoffRequested,
		Severity:   events.SeverityInfo,
		Workspace:  t.Workspace,
		Team:       t.Name,
		Role:       role.Name,
		Session:    sess.Key(),
		Generation: t.Generation,
		Message: fmt.Sprintf("cause=%s: session %s age %s >= %s, %s threshold; notice %s; handoff window %s",
			cond.On, sess.Key(), age.Round(time.Second), cond.MaxAge, stage, delivery, role.Shift.HandoffWindowOrDefault()),
	})
}

// advanceShiftRequest moves a pending request on: drop it when its session is
// gone or replaced, shift on an observed marker, escalate once the window
// expires. It reports whether a shift started.
func (c *Controller) advanceShiftRequest(t *api.Team, role *api.Role, req api.ShiftRequest, now time.Time) bool {
	sess, err := c.store.GetSession(req.Session)
	if err != nil || !sess.State.CountsAsAlive() || !sess.CreatedAt.Before(req.RequestedAt) {
		// The seat asked is gone, or a health restart replaced it under the
		// same name (a session created at or after the request cannot be the
		// one asked, which was past its max age): the new session starts its
		// age over, so the request does not carry to it.
		c.dropShiftRequest(t, role.Name)
		return false
	}
	if req.Escalated {
		return false // the supervisor decides now (D5 step 3)
	}
	if role.Shift.Handoff != "" {
		path := handoffPath(role.Shift.Handoff, sess)
		if handoffComplete(path, role.Shift.HandoffMarker) {
			if c.autoShiftsThisTick >= maxAutoShiftsPerTick {
				return false // the marker stays; a later tick starts the shift
			}
			msg := fmt.Sprintf("cause=%s: session %s wrote its handoff (%s ends in the marker), requested %s",
				req.Cause, sess.Key(), path, req.RequestedAt.Format(time.RFC3339))
			return c.autoShift(t, role, sess.Key(), msg)
		}
	}
	window := role.Shift.HandoffWindowOrDefault()
	if now.Sub(req.RequestedAt) < window {
		return false
	}

	reason := "no handoff path is declared, so marvel cannot observe one"
	if role.Shift.Handoff != "" {
		reason = fmt.Sprintf("no marker at the end of %s", handoffPath(role.Shift.Handoff, sess))
	}
	req.Escalated = true
	if err := c.store.UpdateTeam(t.Key(), func(live *api.Team) error {
		if live.ShiftRequests == nil {
			live.ShiftRequests = make(map[string]api.ShiftRequest)
		}
		live.ShiftRequests[role.Name] = req
		return nil
	}); err != nil {
		log.Printf("shift: %s role %s: record escalation: %v", t.Key(), role.Name, err)
		return false
	}
	t.ShiftRequests[role.Name] = req
	events.Emit(c.Events, events.Event{
		Kind:       events.KindShiftHandoffMissing,
		Severity:   events.SeverityWarning,
		Workspace:  t.Workspace,
		Team:       t.Name,
		Role:       role.Name,
		Session:    sess.Key(),
		Generation: t.Generation,
		Message: fmt.Sprintf("cause=%s: session %s, age %s, asked %s, window %s expired: %s; the seat keeps running and the team's supervisor decides whether to call the shift",
			req.Cause, sess.Key(), now.Sub(sess.CreatedAt).Round(time.Second), req.RequestedAt.Format(time.RFC3339), window, reason),
	})
	return false
}

func (c *Controller) dropShiftRequest(t *api.Team, role string) {
	if err := c.store.UpdateTeam(t.Key(), func(live *api.Team) error {
		delete(live.ShiftRequests, role)
		return nil
	}); err != nil {
		log.Printf("shift: %s role %s: drop handoff request: %v", t.Key(), role, err)
		return
	}
	delete(t.ShiftRequests, role)
}

// handoffPath fills the declared template for one session: {session} is the
// session name the seat sees as MARVEL_SESSION, and a leading ~/ is the
// daemon user's home.
func handoffPath(template string, sess api.Session) string {
	p := strings.ReplaceAll(template, "{session}", sess.Name)
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, rest)
		}
	}
	return p
}

// handoffComplete reports whether the file at path exists and its last
// non-empty line is marker. It reads at most the file's last maxHandoffTail
// bytes, and nothing else on the host (D5 step 2).
func handoffComplete(path, marker string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return false
	}
	off := info.Size() - maxHandoffTail
	if off < 0 {
		off = 0
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return false
	}
	tail, err := io.ReadAll(io.LimitReader(f, maxHandoffTail))
	if err != nil {
		return false
	}
	tail = bytes.TrimRight(tail, " \t\r\n")
	if i := bytes.LastIndexByte(tail, '\n'); i >= 0 {
		tail = tail[i+1:]
	}
	return strings.TrimSpace(string(tail)) == marker
}
