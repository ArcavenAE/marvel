package daemon

import "testing"

// isDismissKey is the one exemption from the pre-inject read: a lone Escape
// dismisses a menu, and anything else typed at one does not. Each condition is
// load-bearing (marvel#559 review): Escape with Enter or as literal text types
// into the menu, so it must still be read first.
func TestIsDismissKeyIsALoneEscapeKeyAndNothingElse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		p    injectParams
		want bool
	}{
		{"a lone Escape key", injectParams{Text: "Escape"}, true},
		{"Escape followed by Enter", injectParams{Text: "Escape", Enter: true}, false},
		{"Escape as literal text", injectParams{Text: "Escape", Literal: true}, false},
		{"Escape as literal text and Enter", injectParams{Text: "Escape", Literal: true, Enter: true}, false},
		{"another key name", injectParams{Text: "Enter"}, false},
		{"a different case", injectParams{Text: "escape"}, false},
		{"Escape with a trailing space", injectParams{Text: "Escape "}, false},
		{"the digit 3", injectParams{Text: "3"}, false},
		{"empty text", injectParams{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isDismissKey(tt.p); got != tt.want {
				t.Errorf("isDismissKey(%+v) = %v, want %v", tt.p, got, tt.want)
			}
		})
	}
}
