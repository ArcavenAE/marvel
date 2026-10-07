package team

import (
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// The view notice (docs/design/readonly-view.md section 5). A refresh that moves
// a seat's view leaves one pending notice on the team, replacing any not yet
// delivered. The controller sends it through Notify on the max-age handoff
// timing: once the pane has been quiet for quiet_for, or at max_defer from the
// first undelivered move however busy. The same refusals apply, and a refused
// notice stays pending and is tried again on the next tick. Delivery and the
// start of the re-entry grace are recorded on the team, so a restart neither
// forgets an undelivered notice nor starts a grace early.

// The origins a Notify caller names. The daemon records them as
// injector=marvel:<origin>.
const (
	NoticeMaxAge     = "max-age"
	NoticeViewNotice = "view-notice"
)

// shortCommit is how many characters of a commit the notice names.
const shortCommit = 12

// viewNoticeKey is the team's key for one seat's view.
func viewNoticeKey(sessKey, view string) string { return sessKey + "/" + view }

// splitViewNoticeKey is the inverse of viewNoticeKey: a view name is one path
// element, so the last slash divides them.
func splitViewNoticeKey(key string) (sessKey, view string) {
	i := strings.LastIndex(key, "/")
	if i < 0 {
		return key, ""
	}
	return key[:i], key[i+1:]
}

func viewNoticeText(view, path, commit string) string {
	sha := commit
	if len(sha) > shortCommit {
		sha = sha[:shortCommit]
	}
	return fmt.Sprintf("marvel: view %s is now %s. If your working directory is under %s, cd to %s again; check with: cat %s/VIEW_SHA",
		view, sha, path, path, path)
}

// NoteViewMoved records that a seat's view moved to commit. It replaces a notice
// not yet delivered, keeping the time of the first undelivered move, so the
// deferral stays bounded however many refreshes happen. A move after a
// delivered notice starts a new one and clears the record for the old commit.
// It is the keeper's hook and takes only the store's lock, never the
// controller's, so a refresh never waits for a reconcile pass.
func (c *Controller) NoteViewMoved(sess api.Session, view, path, previous, commit string) {
	now := c.nowUTC()
	key := viewNoticeKey(sess.Key(), view)
	teamKey := sess.Workspace + "/" + sess.Team
	if err := c.store.UpdateTeam(teamKey, func(live *api.Team) error {
		if live.ViewNotices == nil {
			live.ViewNotices = make(map[string]api.ViewNotice)
		}
		n := live.ViewNotices[key]
		if !n.Pending() {
			n.PendingSince = now
			n.DeliveredAt, n.GraceStart = time.Time{}, time.Time{}
		}
		n.Commit, n.Path = commit, path
		// The tree the view left is superseded and stays readable. The tree it
		// moved onto is current, so it is neither: a view that returns to a
		// commit makes that tree current again.
		n.Superseded = slices.DeleteFunc(n.Superseded, func(c string) bool { return c == commit })
		n.Sealed = slices.DeleteFunc(n.Sealed, func(c string) bool { return c == commit })
		if previous != "" && previous != commit && !slices.Contains(n.Superseded, previous) && !slices.Contains(n.Sealed, previous) {
			n.Superseded = append(n.Superseded, previous)
		}
		live.ViewNotices[key] = n
		return nil
	}); err != nil {
		log.Printf("view notice: %s: record move: %v", key, err)
	}
}

// deliverViewNotices sends each seat's pending notices that are due, records the
// outcome, starts the grace for a delivered notice at the first quiet after it,
// and drops the record of a seat that is gone.
func (c *Controller) deliverViewNotices(t *api.Team) {
	if len(t.ViewNotices) == 0 {
		return
	}
	now := c.nowUTC()
	teamKey := t.Key()
	for _, key := range slices.Sorted(maps.Keys(t.ViewNotices)) {
		n := t.ViewNotices[key]
		sessKey, view := splitViewNoticeKey(key)
		sess, err := c.store.GetSession(sessKey)
		if err != nil || !sess.State.CountsAsAlive() {
			c.updateViewNotice(teamKey, key, func(live map[string]api.ViewNotice, _ api.ViewNotice) { delete(live, key) })
			continue
		}
		lastActive := sess.CreatedAt
		if sess.ContextAt.After(lastActive) {
			lastActive = sess.ContextAt
		}
		quiet := now.Sub(lastActive) >= api.DefaultShiftQuietFor

		if n.Pending() {
			if sess.State != api.SessionRunning {
				continue
			}
			if !quiet && now.Sub(n.PendingSince) < api.DefaultShiftMaxDefer {
				continue
			}
			sent := n
			notifyErr := c.sendViewNotice(sess, viewNoticeText(view, n.Path, n.Commit))
			c.updateViewNotice(teamKey, key, func(live map[string]api.ViewNotice, cur api.ViewNotice) {
				if cur.Commit != sent.Commit {
					return // it moved while we sent; the new notice goes next tick
				}
				if notifyErr != nil {
					cur.Undelivered = notifyErr.Error()
				} else {
					cur.Undelivered = ""
					cur.DeliveredCommit, cur.DeliveredAt, cur.GraceStart = sent.Commit, now, time.Time{}
				}
				live[key] = cur
			})
			continue
		}
		if n.DeliveredAt.IsZero() {
			continue
		}
		graceStart := n.GraceStart
		if graceStart.IsZero() && quiet {
			delivered := n.DeliveredCommit
			c.updateViewNotice(teamKey, key, func(live map[string]api.ViewNotice, cur api.ViewNotice) {
				if cur.DeliveredCommit != delivered || !cur.GraceStart.IsZero() || cur.Pending() {
					return
				}
				cur.GraceStart = now
				live[key] = cur
			})
			graceStart = now
		}
		if !graceStart.IsZero() && now.Sub(graceStart) >= viewGrace(t, sess, view) {
			c.sealSuperseded(teamKey, key, sess, view, n)
		}
	}
}

func (c *Controller) sendViewNotice(sess api.Session, text string) error {
	if c.Notify == nil {
		return fmt.Errorf("no notifier")
	}
	return c.Notify(sess, text, NoticeViewNotice)
}

// ViewTreesHeld is how many superseded trees of a seat's view are still
// readable. The keeper pauses the view's refresh at view.MaxHeldTrees.
func (c *Controller) ViewTreesHeld(sess api.Session, view string) int {
	t, err := c.store.GetTeam(sess.Workspace + "/" + sess.Team)
	if err != nil {
		return 0
	}
	return len(t.ViewNotices[viewNoticeKey(sess.Key(), view)].Superseded)
}

// updateViewNotice applies fn to the live record of one notice under the
// store's lock, so a move recorded meanwhile is never overwritten.
func (c *Controller) updateViewNotice(teamKey, key string, fn func(live map[string]api.ViewNotice, cur api.ViewNotice)) {
	if err := c.store.UpdateTeam(teamKey, func(live *api.Team) error {
		cur, ok := live.ViewNotices[key]
		if !ok {
			return nil
		}
		fn(live.ViewNotices, cur)
		return nil
	}); err != nil {
		log.Printf("view notice: %s: record: %v", key, err)
	}
}

// viewGrace is the re-entry grace the seat's role declares for a view. A view
// the role no longer declares gets the default.
func viewGrace(t *api.Team, sess api.Session, view string) time.Duration {
	for _, r := range t.Roles {
		if r.Name != sess.Role {
			continue
		}
		for _, v := range r.Views {
			if v.Name == view {
				return v.ReenterGrace
			}
		}
	}
	return api.DefaultViewReenterGrace
}

// sealSuperseded seals the trees older than the commit the delivered notice
// named, once its grace has ended. The tree that commit names stays readable
// until a later notice is delivered. A seal that fails is logged and left for
// the next tick, and never holds back a swap or a spawn.
func (c *Controller) sealSuperseded(teamKey, key string, sess api.Session, view string, n api.ViewNotice) {
	if c.SealViewTrees == nil || len(n.Superseded) == 0 {
		return
	}
	older := n.Superseded
	if i := slices.Index(n.Superseded, n.DeliveredCommit); i >= 0 {
		older = n.Superseded[:i]
	}
	if len(older) == 0 {
		return
	}
	older = slices.Clone(older)
	if err := c.SealViewTrees(sess, view, older); err != nil {
		log.Printf("view notice: %s: seal %d superseded trees: %v", key, len(older), err)
		return
	}
	c.updateViewNotice(teamKey, key, func(live map[string]api.ViewNotice, cur api.ViewNotice) {
		for _, commit := range older {
			if i := slices.Index(cur.Superseded, commit); i >= 0 {
				cur.Superseded = slices.Delete(cur.Superseded, i, i+1)
				cur.Sealed = append(cur.Sealed, commit)
			}
		}
		live[key] = cur
	})
}
