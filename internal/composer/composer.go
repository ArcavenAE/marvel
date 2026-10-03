// Package composer reads what state a harness's input composer is in from a
// pane capture, so an inject can be checked by its effect and a clear is only
// ever sent where it is safe (aae-orc-dgb35, aae-orc-g88i1).
//
// The reading is deliberately a separate, pure step from the capture: a Reader
// sees one plain capture string and returns a State, so it is testable without
// tmux and a harness upgrade changes one reader.
package composer

import (
	"errors"
	"path/filepath"
	"strings"
)

// State is what a composer is showing.
type State string

const (
	// Empty is an idle composer holding no draft.
	Empty State = "empty"
	// HoldsText is an idle composer with a staged draft in it.
	HoldsText State = "holds_text"
	// MidTurn is a harness working on a turn. An empty composer does NOT mean
	// idle: while text streams the composer can read empty.
	MidTurn State = "mid_turn"
	// MenuUnsafe is a menu or prompt where Enter, a newline or a key does
	// something other than edit a draft (codex's update menu runs the vendor's
	// installer on one Enter, marvel#477).
	MenuUnsafe State = "menu_unsafe"
	// Shell is a shell prompt with no harness in the foreground.
	Shell State = "shell"
	// Unknown is anything a reader cannot place. It is never treated as safe.
	Unknown State = "unknown"
)

// Reader reads one harness's composer.
type Reader interface {
	// Name is the harness this reader is for.
	Name() string
	// Read places a plain pane capture in a State. A reader that cannot tell
	// returns Unknown, never a guess.
	Read(capture string) State
	// Escapes reports whether Read needs the capture taken with escape
	// sequences (tmux capture-pane -e): a reader that tells a placeholder from
	// typed text by its attributes does.
	Escapes() bool
	// Preflight reports whether every inject to this harness is preceded by a
	// capture and refused on MenuUnsafe: true where the harness has a state in
	// which a keystroke is dangerous.
	Preflight() bool
	// ClearKey is the tmux key that discards a staged draft, or "" when marvel
	// must never clear this harness. Codex and opencode exit on C-c at an empty
	// composer, so a clear there would end the seat if the capture were misread
	// or the draft vanished between the read and the key: they have no clear key.
	ClearKey() string
}

// ReaderFor returns the reader for a runtime (harness) name. A runtime without
// a contract yet gets a reader that reads Unknown and never clears; Unknown is
// never treated as safe by a caller.
func ReaderFor(runtime string) Reader {
	// The harness is the program, so a path or a wrapper directory does not hide
	// it: "/opt/homebrew/bin/codex" is codex.
	switch filepath.Base(strings.TrimSpace(runtime)) {
	case "codex":
		return codexReader{}
	case "claude":
		return claudeReader{}
	}
	return unknownReader{}
}

var shells = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "fish": true,
	"ksh": true, "tcsh": true, "csh": true,
}

// ShellTarget reports whether a foreground command name is a shell, which
// means no harness is running in the pane.
func ShellTarget(name string) bool {
	return shells[strings.TrimPrefix(name, "-")]
}

type unknownReader struct{}

func (unknownReader) Name() string      { return "unknown" }
func (unknownReader) Read(string) State { return Unknown }
func (unknownReader) Escapes() bool     { return false }
func (unknownReader) Preflight() bool   { return false }
func (unknownReader) ClearKey() string  { return "" }

// codexHazards are the lines of codex's startup "Update available" menu and of
// the installer it starts (measured on codex-cli 0.157.0, marvel#477). The
// menu's default option runs the vendor's curl | sh installer on one Enter, so a
// capture that shows any of them is refused. A false positive (chat text that
// quotes the menu) refuses an inject, which is the safe direction.
var codexHazards = []string{"Update available", "Update now", "Updating Codex", "install.sh"}

// codexReader reads codex. It recognizes the one state research has measured as
// dangerous; every other capture is Unknown until the codex composer states are
// grounded (aae-orc-g88i1). It has no clear key: C-c on an empty codex composer
// exits the harness.
type codexReader struct{}

func (codexReader) Name() string { return "codex" }

func (codexReader) Read(capture string) State {
	// Wrapping can break the menu's words across lines and indents; the words
	// are matched with any run of whitespace between them taken as one space.
	text := strings.Join(strings.Fields(capture), " ")
	for _, h := range codexHazards {
		if strings.Contains(text, h) {
			return MenuUnsafe
		}
	}
	return Unknown
}

func (codexReader) Escapes() bool    { return false }
func (codexReader) Preflight() bool  { return true }
func (codexReader) ClearKey() string { return "" }

// Intent is what an inject meant to do, so its effect can be checked.
type Intent string

const (
	// Submit sends the draft: the composer should read empty afterwards, or the
	// harness should be mid-turn.
	Submit Intent = "submit"
	// Stage types text without sending it: the composer should hold it.
	Stage Intent = "stage"
)

// Confirms reports whether a composer state is the expected effect of an
// intent. Unknown, MenuUnsafe and Shell never confirm anything: a reading that
// cannot place the composer is not evidence that the inject landed.
func Confirms(i Intent, s State) bool {
	switch i {
	case Submit:
		return s == Empty || s == MidTurn
	case Stage:
		return s == HoldsText
	}
	return false
}

// CanClear says whether a clear may be sent to a composer a reader just read.
// It needs a clear key and a composer known to hold text. A composer that reads
// anything else, Unknown included, is refused: on codex and opencode the clear
// key exits the harness when the composer is really empty, so a misread would
// end the seat.
func CanClear(r Reader, s State) error {
	if r.ClearKey() == "" {
		return errors.New("no safe clear key is known for " + r.Name())
	}
	if s != HoldsText {
		return errors.New("the composer reads " + string(s) + ", not " + string(HoldsText))
	}
	return nil
}
