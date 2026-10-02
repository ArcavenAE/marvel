// Package composer reads what state a harness's input composer is in from a
// pane capture, so an inject can be checked by its effect and a clear is only
// ever sent where it is safe (aae-orc-dgb35, aae-orc-g88i1).
//
// The reading is deliberately a separate, pure step from the capture: a Reader
// sees one plain capture string and returns a State, so it is testable without
// tmux and a harness upgrade changes one reader.
package composer

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

// ReaderFor returns the reader for a runtime (harness) name. An unrecognized
// name gets a reader that reads Unknown and never clears.
func ReaderFor(runtime string) Reader {
	return unknownReader{}
}

// ShellTarget reports whether a foreground command name is a shell, which
// means no harness is running in the pane.
func ShellTarget(name string) bool {
	return false
}

type unknownReader struct{}

func (unknownReader) Name() string      { return "unknown" }
func (unknownReader) Read(string) State { return Unknown }
func (unknownReader) Preflight() bool   { return false }
func (unknownReader) ClearKey() string  { return "" }
