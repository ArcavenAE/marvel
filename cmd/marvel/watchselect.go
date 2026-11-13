package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"

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

// daemonAttachTarget is what Enter resolves a desk against in the
// single-cluster watch: the tmux socket the selected daemon reports, and the
// host when it is not on this machine. It asks the daemon, so a daemon that
// is not answering holds the key until its own timeout.
func daemonAttachTarget(string) (attachTarget, error) {
	addr, _, _, err := resolveDaemonRung()
	if err != nil {
		return attachTarget{}, err
	}
	st, err := fetchDaemonStatus()
	if err != nil {
		return attachTarget{}, err
	}
	host, remote := attachHost(addr)
	return attachTarget{Socket: st.TmuxSocket, Remote: remote, Host: host}, nil
}

// sessionsView is renderSessionsView for the watch screen: the selected row is
// marked, and when the screen has a height the table is cut to the rows that
// fit, scrolled only as far as keeps the selected row on screen. used is the
// frame so far, whose lines count against the height.
func (ws *watchSort) sessionsView(sessions []api.Session, header string, fit fitOptions, used string) string {
	pre, table, post := renderSessionsParts(sessions, ws.columns, header, fit)
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

	lo, hi := 0, len(rows)
	h := ws.height
	if ws.heightFn != nil {
		h = ws.heightFn()
	}
	if h > 0 {
		fixed := strings.Count(used, "\n") + strings.Count(pre, "\n") + 1 + strings.Count(post, "\n")
		if ws.status != "" {
			fixed++
		}
		budget := max(h-fixed, 1)
		top := ws.top
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
	b.WriteString(post)
	return b.String()
}

// writeStatus ends the frame with the status line, when there is one.
func (ws *watchSort) writeStatus(buf *bytes.Buffer) {
	if ws.status != "" {
		buf.WriteString(ws.status + "\n")
	}
}
