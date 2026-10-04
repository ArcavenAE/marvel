package limitmenu

import "strings"

// testSample is SYNTHETIC and test-only. It is not a harness capture: the
// words follow the probe brief's description of the menu, and the glyph and
// column are invented. No production code uses it.
func testSample() Sample {
	return Sample{
		Version: "test-only-0",
		Glyph:   "❯",
		Rows: []string{
			"What do you want to do?",
			"❯ 1. Stop and wait for limit to reset",
			"  2. Wait here, then continue automatically at Oct 6 at 9pm",
			"  3. Switch to usage credits",
			"Enter to confirm · Esc to cancel",
		},
		Options: [3]int{1, 2, 3},
	}
}

// screen draws the test menu with the cursor on option cursor (1 to 3, or 0
// for none, or 4 for all three) and the given time span, under some history.
func screen(cursor int, span string) string {
	rows := []string{
		"> earlier output",
		"",
		"What do you want to do?",
		row(1, cursor, "1. Stop and wait for limit to reset"),
		row(2, cursor, "2. Wait here, then continue automatically at "+span),
		row(3, cursor, "3. Switch to usage credits"),
		"Enter to confirm · Esc to cancel",
		"",
		"",
	}
	return strings.Join(rows, "\n")
}

func row(n, cursor int, text string) string {
	if cursor == n || cursor == 4 {
		return "❯ " + text
	}
	return "  " + text
}
