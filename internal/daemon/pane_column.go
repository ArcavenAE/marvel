package daemon

import (
	"regexp"
	"strings"
	"sync"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/composer"
)

// The PANE column of `get sessions` (marvel#1142 part a): one word for what a
// seat's pane is doing, so a seat sitting at a login or startup prompt no
// longer reads as alive. The set is the operator's (ruling I-60): working,
// idle, prompt, restart, logged-out, unknown.
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
//   - restart is claude's "Update installed · Restart to update" banner.
//
// A word that cannot be told from another is unknown; nothing is guessed. The
// read is the same read-only path as COMPOSER (a foreground query and one
// capture, nothing typed, a client-workspace seat never captured).

// The PANE words.
const (
	paneWorking   = "working"
	paneIdle      = "idle"
	panePrompt    = "prompt"
	paneRestart   = "restart"
	paneLoggedOut = "logged-out"
	paneUnknown   = "unknown"
)

// gapShell is the fixed reason a seat whose pane is at a shell reads unknown.
const gapShell = "pane at a shell, no harness in front"

// restartBannerRows is how many of the screen's last non-blank rows are
// searched for the banner. It sat 5 rows above the bottom of a 24 row screen
// on the panes read for #1142.
const restartBannerRows = 8

// claudeRestartBanner is the end of the row claude draws once an update is
// installed: a check mark, then the words. A row may carry a hint before it
// ("new task? /clear to save 119k tokens · ✔ Update installed ..."), so the row
// is matched at its end, not from its start. The check mark is required: the
// plain words in chat text, or in a command that quotes them, do not match.
var claudeRestartBanner = regexp.MustCompile(`✔ Update installed · Restart to update\s*$`)

var sgrSequence = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

// hasRestartBanner reports whether one of the last rows of a capture is the
// claude restart banner. The capture may carry SGR escapes.
func hasRestartBanner(capture string) bool {
	rows := strings.Split(sgrSequence.ReplaceAllString(capture, ""), "\n")
	seen := 0
	for i := len(rows) - 1; i >= 0 && seen < restartBannerRows; i-- {
		row := strings.TrimRight(rows[i], " \t\r")
		if row == "" {
			continue
		}
		seen++
		if claudeRestartBanner.MatchString(row) {
			return true
		}
	}
	return false
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
	case composer.Empty, composer.HoldsText:
		return paneIdle, ""
	}
	return paneUnknown, gapReaderUnsure
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
