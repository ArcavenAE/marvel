package composer

import "testing"

func TestHasDim(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		capture string
		want    bool
	}{
		{"dim text", "❯ \x1b[2mstart with step 1\x1b[0m\n", true},
		{"dim among other attributes", "\x1b[1;2;37mhint\x1b[0m", true},
		{"dim on one line, plain on the next", "plain\n\x1b[2mfaint\x1b[0m\nplain", true},
		{"no escapes at all", "❯ typed draft\n", false},
		{"bold and colour only", "\x1b[1mbold\x1b[0m \x1b[38;5;2mgreen\x1b[0m", false},
		{"a 2 inside a 256-colour selector is a colour", "\x1b[38;5;2mgreen\x1b[0m", false},
		{"a 2 inside a truecolour selector is not dim", "\x1b[38;2;2;2;2mrgb\x1b[0m", false},
		{"a 2 inside a 256-colour background selector is a colour", "\x1b[48;5;2mbg\x1b[0m", false},
		{"an empty SGR resets dim before the text", "\x1b[2m\x1b[mtext", false},
		{"dim ended by 22 before the text", "\x1b[2m\x1b[22mtext", false},
		{"dim reset by 0 before the text", "\x1b[2m\x1b[0mtext", false},
		{"dim set and reset around nothing visible", "\x1b[2m   \x1b[0m", false},
		{"empty", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := HasDim(tc.capture); got != tc.want {
				t.Errorf("HasDim(%q) = %v, want %v", tc.capture, got, tc.want)
			}
		})
	}
}
