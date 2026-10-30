package daemon

import (
	"regexp"
	"sync"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/composer"
)

// The PANE column of `get sessions` (marvel#1142 part a): one word for what a
// seat's pane is doing, so a seat sitting at a login or startup prompt no
// longer reads as alive. The set is the operator's (ruling I-60): working,
// idle, prompt, restart, logged-out, unknown, and fresh (part d, ruled by the
// director: a seat that has never taken a turn, with no startup text injected
// by marvel).
//
// Where each word comes from:
//   - logged-out and prompt are the harness-state watchdog's stored verdict
//     (Session.HarnessState, logged-out and parked). No capture is made.
//   - working and idle are the composer reader's verdict on the visible
//     screen (mid_turn; empty or holds_text). A harness with no reader, or a
//     screen the reader cannot place, reads unknown. Codex's reader grounds no
//     working or idle state, so a codex seat reads unknown unless it is at a
//     prompt.
//   - prompt also covers the composer reader's menu_unsafe, the one prompt a
//     reader recognizes without the watchdog (codex's update menu).
//   - restart is claude's "Update installed · Restart to update" banner, on the
//     row directly above the composer box.
//
// A word that cannot be told from another is unknown; nothing is guessed. The
// read is the same read-only path as COMPOSER (a foreground query and one
// capture, nothing typed, a client-workspace seat never captured).

// The PANE words.
const (
	paneWorking   = "working"
	paneIdle      = "idle"
	paneFresh     = "fresh"
	panePrompt    = "prompt"
	paneRestart   = "restart"
	paneLoggedOut = "logged-out"
	paneUnknown   = "unknown"
)

// gapShell is the fixed reason a seat whose pane is at a shell reads unknown.
const gapShell = "pane at a shell, no harness in front"

// claudeRestartBanner is the row claude draws directly above its composer box
// once an update is installed. Two shapes were read on live panes: the banner
// alone, right-aligned (at least 8 leading spaces), and the banner after a
// "new task?" hint. The check mark is required, the row must end with the
// words, and the match is on that one row, so the plain words in chat text or
// in a command that quotes them do not match.
var claudeRestartBanner = regexp.MustCompile(
	`^(?: {8,}|  new task\? /clear to save [0-9.]+[kM]? to… · )✔ Update installed · Restart to update$`)

// hasRestartBanner reports whether the row directly above the composer's top
// rule is the claude restart banner. A transcript row is never that row: the
// composer box sits below the transcript, so a row above it that is not the
// banner is the last transcript row, and only the banner's own shapes match.
// The capture may carry SGR escapes.
func hasRestartBanner(capture string) bool {
	row, ok := composer.ClaudeRowAboveComposer(capture)
	return ok && claudeRestartBanner.MatchString(row)
}

// paneWord is the PANE value for one session, with the reason when it is
// unknown. "" means not read: the session has no running pane.
func paneWord(pane composerPane, s api.Session, client bool, clientErr error) (word, gap string) {
	if s.PaneID == "" || s.State != api.SessionRunning {
		return "", ""
	}
	// The watchdog's stored verdict needs no capture and shows nothing new.
	if hs := s.HarnessState; hs != nil {
		switch hs.State {
		case api.HarnessStateLoggedOut:
			return paneLoggedOut, ""
		case api.HarnessStateParked:
			return panePrompt, ""
		}
	}
	if clientErr != nil {
		return paneUnknown, gapManifest
	}
	if client {
		return paneUnknown, gapClient
	}
	reader := composer.ReaderFor(s.Runtime.Name)
	if reader.Name() == "unknown" {
		return paneUnknown, gapNoReader + s.Runtime.Name
	}
	cmd, _, err := pane.PaneForeground(s.PaneID)
	if err != nil {
		return paneUnknown, gapCapture
	}
	if composer.ShellTarget(cmd) {
		return paneUnknown, gapShell
	}
	capture := pane.CapturePane
	if reader.Escapes() {
		capture = pane.CapturePaneEscapes
	}
	content, err := capture(s.PaneID)
	if err != nil {
		return paneUnknown, gapCapture
	}
	st := reader.Read(content)
	switch st {
	case composer.MidTurn:
		// A turn is open: the banner can sit on a seat that is still working.
		return paneWorking, ""
	case composer.MenuUnsafe:
		return panePrompt, ""
	}
	if reader.Name() == "claude" && hasRestartBanner(content) {
		return paneRestart, ""
	}
	switch st {
	case composer.Empty:
		if neverTookATurn(s) {
			return paneFresh, ""
		}
		return paneIdle, ""
	case composer.HoldsText:
		return paneIdle, ""
	}
	return paneUnknown, gapReaderUnsure
}

// neverTookATurn reports whether marvel can see a seat's activity and has seen
// none: no context reading with tokens, requests or occupancy. A seat marvel
// has no activity channel for is never fresh, because a seat that did work
// would read the same, so it stays idle. A seat whose first turn is still ahead
// of it reads fresh until the first reading lands.
func neverTookATurn(s api.Session) bool {
	return s.ActiveTicks.Observable &&
		s.ContextTokens == 0 && s.ContextRequests == 0 && s.ContextPercent == 0
}

// stampPane puts the PANE word, and the reason for an unknown, on each served
// copy. client is the same source COMPOSER uses.
func stampPane(pane composerPane, sessions []api.Session, client func(workspace string) (bool, error)) {
	for i := range sessions {
		c, err := client(sessions[i].Workspace)
		sessions[i].Pane, sessions[i].PaneGap = paneWord(pane, sessions[i], c, err)
	}
}

// memoPane remembers each capture for the length of one listing, so the
// COMPOSER and PANE columns of one `get sessions` share one capture per pane.
type memoPane struct {
	composerPane
	mu   sync.Mutex
	seen map[string]captured
}

type captured struct {
	out string
	err error
}

func newMemoPane(p composerPane) *memoPane {
	return &memoPane{composerPane: p, seen: map[string]captured{}}
}

func (m *memoPane) memo(key string, read func() (string, error)) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.seen[key]; ok {
		return c.out, c.err
	}
	out, err := read()
	m.seen[key] = captured{out, err}
	return out, err
}

func (m *memoPane) CapturePane(paneID string) (string, error) {
	return m.memo("p:"+paneID, func() (string, error) { return m.composerPane.CapturePane(paneID) })
}

func (m *memoPane) CapturePaneEscapes(paneID string) (string, error) {
	return m.memo("e:"+paneID, func() (string, error) { return m.composerPane.CapturePaneEscapes(paneID) })
}
