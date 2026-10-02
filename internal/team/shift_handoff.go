package team

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
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

// firstSessionOverAge returns the first running session of role, at any
// generation (a role's seats carry the generation of the last shift that
// covered the role, not the team's counter, marvel#451), that is past cond.MaxAge and either quiet for cond.QuietFor (the quiet
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
	for _, s := range c.store.ListSessionsByTeamRole(t.Workspace, t.Name, role.Name) {
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
		if path, err := handoffPath(role.Shift.Handoff, sess); err == nil {
			text += fmt.Sprintf(" to %s, ending with the line %q", path, role.Shift.HandoffMarker)
		}
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
		// The supervisor decides now (D5 step 3), and marvel keeps saying so:
		// the event ring is in memory, so one emission can be lost to a
		// restart or a wrap, and the escalation is sticky (marvel#453).
		c.repeatHandoffMissing(t, role, sess, now)
		return false
	}
	path, pathErr := "", error(nil)
	if role.Shift.Handoff != "" {
		path, pathErr = handoffPath(role.Shift.Handoff, sess)
	}
	if path != "" && pathErr == nil {
		if c.handoffProbes.complete(sess.Key(), path, role.Shift.HandoffMarker) {
			if c.autoShiftsThisTick >= maxAutoShiftsPerTick {
				return false // the marker stays; a later tick starts the shift
			}
			msg := fmt.Sprintf("cause=%s: session %s wrote its handoff (%s ends in the marker), requested %s",
				req.Cause, sess.Key(), path, req.RequestedAt.Format(time.RFC3339))
			c.handoffProbes.forget(sess.Key())
			return c.autoShift(t, role, sess.Key(), msg)
		}
	}
	window := role.Shift.HandoffWindowOrDefault()
	if now.Sub(req.RequestedAt) < window {
		return false
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
	c.emitHandoffMissing(t, role, sess, req, now, false)
	return false
}

// handoffMissingReason says why no marker was observed.
func handoffMissingReason(role *api.Role, sess api.Session) string {
	if role.Shift.Handoff == "" {
		return "no handoff path is declared, so marvel cannot observe one"
	}
	path, err := handoffPath(role.Shift.Handoff, sess)
	if err != nil {
		return fmt.Sprintf("the handoff path cannot be resolved (%v)", err)
	}
	return fmt.Sprintf("no regular file at %s ends in the marker", path)
}

// emitHandoffMissing emits the escalation and starts the repeat clock.
func (c *Controller) emitHandoffMissing(t *api.Team, role *api.Role, sess api.Session, req api.ShiftRequest, now time.Time, repeat bool) {
	if c.missingEmitted == nil {
		c.missingEmitted = make(map[string]time.Time)
	}
	c.missingEmitted[sess.Key()] = now
	again := ""
	if repeat {
		again = " (repeated: the request is still escalated)"
	}
	events.Emit(c.Events, events.Event{
		Kind:       events.KindShiftHandoffMissing,
		Severity:   events.SeverityWarning,
		Workspace:  t.Workspace,
		Team:       t.Name,
		Role:       role.Name,
		Session:    sess.Key(),
		Generation: t.Generation,
		Message: fmt.Sprintf("cause=%s: session %s, age %s, asked %s, window %s expired: %s; the seat keeps running and the team's supervisor decides whether to call the shift%s",
			req.Cause, sess.Key(), now.Sub(sess.CreatedAt).Round(time.Second), req.RequestedAt.Format(time.RFC3339), role.Shift.HandoffWindowOrDefault(), handoffMissingReason(role, sess), again),
	})
}

// repeatHandoffMissing says it again once per handoff window while the request
// stays escalated. The clock is in memory, so a daemon restart says it at once
// on the next tick, which is the point.
func (c *Controller) repeatHandoffMissing(t *api.Team, role *api.Role, sess api.Session, now time.Time) {
	if last, ok := c.missingEmitted[sess.Key()]; ok && now.Sub(last) < role.Shift.HandoffWindowOrDefault() {
		return
	}
	c.emitHandoffMissing(t, role, sess, t.ShiftRequests[role.Name], now, true)
}

func (c *Controller) dropShiftRequest(t *api.Team, role string) {
	if req, ok := t.ShiftRequests[role]; ok {
		c.handoffProbes.forget(req.Session)
		delete(c.missingEmitted, req.Session)
	}
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
//
// The template is checked for a .. element at apply, but {session} is filled
// here from the session name, which carries the team and role names. A name
// that is not exactly one path element is refused, so it cannot steer the
// resolved path out of the directory the manifest declared (marvel#444).
func handoffPath(template string, sess api.Session) (string, error) {
	if strings.Contains(template, "{session}") {
		if n := sess.Name; n == "" || n == "." || n == ".." || strings.ContainsAny(n, `/\`) {
			return "", fmt.Errorf("session name %q is not one path element", n)
		}
	}
	p := strings.ReplaceAll(template, "{session}", sess.Name)
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve ~/ in handoff path: %w", err)
		}
		p = filepath.Join(home, rest)
	}
	return p, nil
}

// handoffComplete reports whether the file at path is a regular file whose
// last non-empty line is marker. The path is written by the seat, and this
// runs under c.mu, so it must not block: a FIFO would hold a plain open
// forever and wedge every team (review 5392253372). So the path must be a
// regular file, not a symlink; it is opened non-blocking without following
// links, and the open file must be the regular file that was checked. It
// reads at most the file's last maxHandoffTail bytes (D5 step 2).
func handoffComplete(path, marker string) bool {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() {
		return false
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(before, info) {
		return false
	}
	off := info.Size() - maxHandoffTail
	if off < 0 {
		off = 0
	}
	tail, err := io.ReadAll(io.NewSectionReader(f, off, maxHandoffTail))
	if err != nil {
		return false
	}
	tail = bytes.TrimRight(tail, " \t\r\n")
	if i := bytes.LastIndexByte(tail, '\n'); i >= 0 {
		tail = tail[i+1:]
	}
	return strings.TrimSpace(string(tail)) == marker
}

// handoffProbes runs the marker read off the controller lock (marvel#444).
// The tick runs under c.mu, and the handoff file is written by the seat, so a
// slow filesystem would stall every team for the read. complete starts at most
// one read per session in the background and answers from the last finished
// one, so a marker is applied one tick after it is written. The zero value is
// ready to use. The handoff file is expected on a local filesystem.
type handoffProbes struct {
	// check reads the file; nil means handoffComplete.
	check func(path, marker string) bool

	mu       sync.Mutex
	done     map[string]handoffProbeResult
	inflight map[string]*struct{}
	wg       sync.WaitGroup
}

type handoffProbeResult struct {
	path, marker string
	complete     bool
}

// complete reports the last finished read of path for session key, false while
// none has finished, and starts a fresh read unless one is already running.
func (p *handoffProbes) complete(key, path, marker string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	last, ok := p.done[key]
	known := ok && last.path == path && last.marker == marker
	if p.inflight[key] == nil {
		if p.inflight == nil {
			p.inflight = make(map[string]*struct{})
		}
		token := &struct{}{}
		p.inflight[key] = token
		check := p.check
		if check == nil {
			check = handoffComplete
		}
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			got := check(path, marker)
			p.mu.Lock()
			defer p.mu.Unlock()
			if p.inflight[key] != token {
				return // forgotten while reading
			}
			delete(p.inflight, key)
			if p.done == nil {
				p.done = make(map[string]handoffProbeResult)
			}
			p.done[key] = handoffProbeResult{path: path, marker: marker, complete: got}
		}()
	}
	return known && last.complete
}

// forget drops what is held for session key, so a request that ended leaves
// nothing behind.
func (p *handoffProbes) forget(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.done, key)
	delete(p.inflight, key)
}

// wait blocks until no read is in flight. Tests use it.
func (p *handoffProbes) wait() { p.wg.Wait() }
