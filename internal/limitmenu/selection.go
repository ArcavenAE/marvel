package limitmenu

import (
	"errors"
	"fmt"
)

// Selection is one measurement from probe P-UL7 (design 9.7): the key pressed
// on a live limit menu, the option row the cursor was on, which option the
// screen then showed as taken, and that screen. It is the "post-selection
// sample". Without one for a harness version, the limit action sends no key.
type Selection struct {
	Version string
	// Key is what was pressed. The limit action acts only on "2".
	Key string
	// FromOption is the option row the cursor was on when Key was pressed.
	FromOption int
	// Took is the option the screen showed as taken: 2 when the second option
	// was taken, 1 or 3 when another was, 0 when none could be told.
	Took int
	// After is the screen shown after the press.
	After Screen
}

// Validate reports whether the selection can be acted on.
func (s Selection) Validate() error {
	if s.Version == "" {
		return errors.New("limitmenu: selection has no harness version")
	}
	if s.Key == "" {
		return errors.New("limitmenu: selection has no key")
	}
	if s.FromOption < 1 || s.FromOption > 3 {
		return fmt.Errorf("limitmenu: selection cursor row %d is not 1 to 3", s.FromOption)
	}
	if s.Took < 0 || s.Took > 3 {
		return fmt.Errorf("limitmenu: selection took option %d", s.Took)
	}
	if s.After.Version != s.Version {
		return errors.New("limitmenu: selection and its screen name different harness versions")
	}
	return s.After.Validate()
}

// SelectsOptionTwo reports whether this measurement shows the digit 2 taking
// the second option.
func (s Selection) SelectsOptionTwo() bool {
	return s.Key == "2" && s.Took == 2 && s.Validate() == nil
}
