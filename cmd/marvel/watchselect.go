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
	ws.detail = ""
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
	// The status line shows the command an operator would type; the raw tmux
	// argv, with its socket path and pane id, is behind the i key.
	line := shortAttachLine(s, ws.cluster)
	ws.detail = quoteArgv(plan.Argv)
	if plan.Exec && ws.term != nil {
		p := plan
		ws.pending, ws.pendingLabel, ws.pendingShort = &p, label, line
	}
	switch {
	case plan.Exec:
		ws.status = "plan: " + line
	case plan.Note != "":
		ws.status = "print only: " + line + " (" + plan.Note + ")"
	default:
		ws.status = "print only: " + line
	}
}

// shortAttachLine is the marvel attach command for a session: its key, and its
// cluster when the row has one. The session key is used, not the pane id,
// because a pane id changes when the seat restarts.
//
// A row's own cluster wins. A watch given one --cluster is not a fan-out, so its
// rows carry none, and watchCluster is the name it was given; without it the
// pasted line would resolve against whatever cluster the other shell has.
func shortAttachLine(s api.Session, watchCluster string) string {
	argv := []string{"marvel", "attach", s.Workspace + "/" + s.Name}
	cluster := s.Cluster
	if cluster == "" {
		cluster = watchCluster
	}
	if cluster != "" {
		argv = append(argv, "--cluster", cluster)
	}
	return quoteArgv(argv)
}

// showDetail puts the raw tmux command of the last plan on the status line.
func (ws *watchSort) showDetail() {
	if ws.detail == "" {
		ws.status = "no plan to show: press Enter on a session first"
		return
	}
	ws.status = "tmux: " + ws.detail
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

// totalsBuilder draws the totals lines for the columns the fit drew. cut says
// the table is cut, so each line ends with the count of the rows it covers
// (design section 2.8). The text is whole lines, each ending in a newline.
type totalsBuilder func(shown []sessionColumn, cut bool) string

// singleTotals is the builder of the one totals line of a single-cluster frame,
// held rows carrying the time they were read. It is nil when the screen shows no
// header, which is where the line lives.
func (ws *watchSort) singleTotals(sessions []api.Session, width int, asOf time.Time) totalsBuilder {
	if !ws.showHeader {
		return nil
	}
	return func(shown []sessionColumn, cut bool) string {
		covered := 0
		if cut {
			covered = len(sessions)
		}
		return wrapTotals(renderTotals(sessions, shown, rateClock(), totalsOpts{asOf: asOf, covered: covered}), width)
	}
}

// sessionsViewTotals is renderSessionsView for the watch screen: the selected
// row is marked, and when the screen has a height the table is cut to the rows
// that fit, scrolled only as far as keeps the selected row on screen. used is
// the frame so far, whose lines count against the height. It can carry the
// totals lines, which count against the height with the rest of the header. The totals cover every
// row, so a cut table says so on the lines; they are built first without that
// suffix, and rebuilt with it when the table is cut, so what is counted in the
// budget is what is drawn (design section 2.8). A nil build carries none.
func (ws *watchSort) sessionsViewTotals(sessions []api.Session, header string, fit fitOptions, used string, build totalsBuilder) string {
	pre, table, post, shown := renderSessionsPartsShown(sessions, ws.columns, header, fit)
	basePre := pre
	withLine := func(cut bool) string {
		if build == nil {
			return basePre
		}
		line := build(shown, cut)
		if header != "" {
			return strings.TrimRight(basePre, "\n") + "\n" + line + "\n"
		}
		return line + basePre
	}
	pre = withLine(false)
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
			// The height is in physical rows: a line wider than the terminal
			// wraps onto more than one (marvel#1598). Table rows share one
			// width, so the widest sets what each costs.
			w := fit.width
			fixed := textPhysicalRows(used, w) + textPhysicalRows(pre, w) + linePhysicalRows(head, w) + textPhysicalRows(post, w)
			if ws.status != "" {
				fixed += linePhysicalRows(ws.status, w)
			}
			per := 1
			for _, r := range rows {
				per = max(per, linePhysicalRows(r, w))
			}
			budget := max((h-fixed)/per, 1)
			cut = false
			if len(rows) > budget {
				budget = max((h-fixed-1)/per, 1) // one line says which rows are shown
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
			if !cut || build == nil || pass == 1 {
				break
			}
			pre = withLine(true) // the suffix only adds height: count it and decide again
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
