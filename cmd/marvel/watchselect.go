package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/arcavenae/marvel/internal/api"
)

// watchSelection is the session the cursor is on, held by identity (cluster,
// workspace, name) and never by row index, so a re-sort or a refresh finds it
// again.
type watchSelection struct {
	set                      bool
	cluster, workspace, name string
}

// highlightOn and highlightOff wrap the selected row in reverse video.
const (
	highlightOn  = "\x1b[7m"
	highlightOff = "\x1b[0m"
)

// terminalHeight is the height of stdout when it is a terminal, else 0 (no
// limit). A seam beside terminalWidth.
var terminalHeight = func() int {
	fd := int(os.Stdout.Fd())
	if !term.IsTerminal(fd) {
		return 0
	}
	_, h, err := term.GetSize(fd)
	if err != nil || h <= 0 {
		return 0
	}
	return h
}

func (sel watchSelection) is(s api.Session) bool {
	return sel.set && s.Cluster == sel.cluster && s.Workspace == sel.workspace && s.Name == sel.name
}

// sessionLabel names a session the way the KEY column does, without the
// cluster when there is none.
func sessionLabel(cluster, workspace, name string) string {
	if cluster == "" {
		return workspace + "/" + name
	}
	return cluster + ":" + workspace + "/" + name
}

// selectedSession is the selected session's row in the rows on hand, or false
// when nothing is selected or the session is no longer listed.
func (ws *watchSort) selectedSession() (api.Session, bool) {
	for _, s := range ws.lastSessions {
		if ws.sel.is(s) {
			return s, true
		}
	}
	return api.Session{}, false
}

// reconcileSelection clears a selection whose session is no longer listed and
// says so on the status line. It never moves the selection to another row.
func (ws *watchSort) reconcileSelection() {
	if !ws.sel.set {
		return
	}
	if _, ok := ws.selectedSession(); ok {
		return
	}
	ws.status = "selected session " + sessionLabel(ws.sel.cluster, ws.sel.workspace, ws.sel.name) + " is gone"
	ws.sel = watchSelection{}
}

// cursorKey applies j, k and Enter. It reports whether the frame changes.
func (ws *watchSort) cursorKey(key byte) bool {
	if key == 13 || key == 10 {
		ws.enter()
		return true
	}
	rows := ws.lastSessions
	if len(rows) == 0 {
		return false
	}
	sortSessions(rows, ws) // the order on screen, which the keys move through
	cur := -1
	for i, s := range rows {
		if ws.sel.is(s) {
			cur = i
		}
	}
	switch {
	case cur < 0:
		cur = 0
	case key == 'j' && cur < len(rows)-1:
		cur++
	case key == 'k' && cur > 0:
		cur--
	}
	s := rows[cur]
	ws.sel = watchSelection{set: true, cluster: s.Cluster, workspace: s.Workspace, name: s.Name}
	ws.status = ""
	return true
}

// enter plans the attach for the selected session and shows it on the status
// line. It runs nothing: the desk is read from the session's current row and
// resolved against the rows of its own cluster, so a desk a refresh gave to
// another session, or one that exists on two clusters, never picks the wrong
// pane.
func (ws *watchSort) enter() {
	s, ok := ws.selectedSession()
	if !ok {
		ws.status = "nothing selected"
		return
	}
	label := sessionLabel(s.Cluster, s.Workspace, s.Name)
	if s.PaneID == "" {
		ws.status = label + " has no desk"
		return
	}
	fail := func(err error) { ws.status = "cannot plan " + label + ": " + err.Error() }
	if ws.target == nil {
		fail(fmt.Errorf("no daemon to resolve against"))
		return
	}
	tgt, err := ws.target(s.Cluster)
	if err != nil {
		fail(err)
		return
	}
	var rows []api.Session
	for _, r := range ws.lastSessions {
		if r.Cluster == s.Cluster {
			rows = append(rows, r)
		}
	}
	plan, err := planAttach(s.PaneID, rows, tgt, os.Getenv("TMUX"), false)
	if err != nil {
		fail(err)
		return
	}
	line := quoteArgv(plan.Argv)
	switch {
	case plan.Exec:
		ws.status = "plan: " + line
	case plan.Note != "":
		ws.status = "print only: " + line + " (" + plan.Note + ")"
	default:
		ws.status = "print only: " + line
	}
}

// watchTargetFor is the target resolver the single-cluster watch installs.
func watchTargetFor(ws *watchSort) func(string) (attachTarget, error) { return ws.headerTarget }

// headerTarget is what Enter resolves a desk against in the single-cluster
// watch: the tmux socket and address from the daemon status the last fetch
// brought back. It makes no call, so Enter never waits on the daemon.
func (ws *watchSort) headerTarget(string) (attachTarget, error) {
	h := ws.header
	if h.Status == nil {
		if h.StatusErr == "" {
			return attachTarget{}, errors.New("no daemon status yet")
		}
		return attachTarget{}, fmt.Errorf("no daemon status yet (%s)", h.StatusErr)
	}
	host, remote := attachHost(h.Address)
	return attachTarget{Socket: h.Status.TmuxSocket, Remote: remote, Host: host}, nil
}

// sessionsView is renderSessionsView for the watch screen: the selected row is
// marked, and when the screen has a height the table is cut to the rows that
// fit, scrolled only as far as keeps the selected row on screen. used is the
// frame so far, whose lines count against the height.
func (ws *watchSort) sessionsView(sessions []api.Session, header string, fit fitOptions, used string) string {
	return ws.sessionsViewTotals(sessions, header, fit, used, false, time.Time{})
}

// sessionsViewTotals is sessionsView that can carry the totals line, which
// counts against the height with the rest of the header. The totals cover every
// row, so a cut table says so on the line; the line is built first without that
// suffix, and rebuilt with it when the table is cut, so the line counted in the
// budget is the line drawn (design section 2.8). asOf stamps rows held from an
// earlier read.
func (ws *watchSort) sessionsViewTotals(sessions []api.Session, header string, fit fitOptions, used string, totals bool, asOf time.Time) string {
	pre, table, post, shown := renderSessionsPartsShown(sessions, ws.columns, header, fit)
	basePre := pre
	withLine := func(covered int) string {
		if !totals {
			return basePre
		}
		line := wrapTotals(renderTotals(sessions, shown, rateClock(), totalsOpts{asOf: asOf, covered: covered}), fit.width)
		if header != "" {
			return strings.TrimRight(basePre, "\n") + "\n" + line + "\n"
		}
		return line + basePre
	}
	pre = withLine(0)
	lines := strings.Split(strings.TrimSuffix(table, "\n"), "\n")
	if len(lines) != len(sessions)+1 {
		return pre + table + post // not one line a row; draw it as it is
	}
	head, rows := lines[0], lines[1:]
	sel := -1
	for i, s := range sessions {
		if ws.sel.is(s) {
			sel = i
		}
	}

	lo, hi, cut := 0, len(rows), false
	h := ws.height
	if ws.heightFn != nil {
		h = ws.heightFn()
	}
	if h > 0 {
		top0 := ws.top
		for pass := 0; pass < 2; pass++ {
			ws.top = top0
			fixed := strings.Count(used, "\n") + strings.Count(pre, "\n") + 1 + strings.Count(post, "\n")
			if ws.status != "" {
				fixed++
			}
			budget := max(h-fixed, 1)
			cut = false
			if len(rows) > budget {
				budget = max(h-fixed-1, 1) // one line says which rows are shown
				cut = true
			}
			top := top0
			if sel >= 0 {
				if sel < top {
					top = sel
				}
				if sel >= top+budget {
					top = sel - budget + 1
				}
			}
			top = max(min(top, len(rows)-budget), 0)
			ws.top = top
			lo, hi = top, min(top+budget, len(rows))
			if !cut || !totals || pass == 1 {
				break
			}
			pre = withLine(len(sessions)) // the suffix only adds height: count it and decide again
		}
	}

	var b bytes.Buffer
	b.WriteString(pre)
	b.WriteString(head + "\n")
	for i := lo; i < hi; i++ {
		if i == sel {
			b.WriteString(highlightOn + rows[i] + highlightOff + "\n")
			continue
		}
		b.WriteString(rows[i] + "\n")
	}
	if cut {
		fmt.Fprintf(&b, "showing rows %d-%d of %d\n", lo+1, hi, len(rows))
	}
	b.WriteString(post)
	return b.String()
}

// writeStatus ends the frame with the status line, when there is one.
func (ws *watchSort) writeStatus(buf *bytes.Buffer) {
	if ws.status != "" {
		buf.WriteString(ws.status + "\n")
	}
}
