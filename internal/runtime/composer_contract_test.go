package runtime

import (
	"slices"
	"strings"
	"testing"
)

// The contract is measured, not assumed: each line names the probe it came from
// and the harness version. These tests pin the values and that every line has a
// citation, so a value cannot be edited without its evidence being looked at.
func TestComposerContracts(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	cases := []struct {
		name                                 string
		clearKey                             string
		exitsEmpty, exitsMidTurn, savesDraft bool
		interrupt                            []string
		version                              string
		interruptSource                      string
	}{
		{"claude", "C-c", false, false, false, []string{"Escape"}, "2.1.288", "not measured"},
		{"codex", "C-c", true, false, true, []string{"Escape"}, "0.157.0", "measured"},
		{"opencode", "C-c", true, true, false, []string{"Escape", "Escape"}, "1.18.15", "measured"},
	}
	for _, tc := range cases {
		c := r.ComposerFor(tc.name)
		if c.Submit != SubmitPasteEnter {
			t.Errorf("%s: Submit = %q, want %q", tc.name, c.Submit, SubmitPasteEnter)
		}
		if c.ClearKey != tc.clearKey {
			t.Errorf("%s: ClearKey = %q, want %q", tc.name, c.ClearKey, tc.clearKey)
		}
		if c.ClearExitsWhenEmpty != tc.exitsEmpty || c.ClearExitsMidTurn != tc.exitsMidTurn || c.ClearSavesDraft != tc.savesDraft {
			t.Errorf("%s: hazards = empty %v midturn %v saves %v, want %v %v %v", tc.name,
				c.ClearExitsWhenEmpty, c.ClearExitsMidTurn, c.ClearSavesDraft, tc.exitsEmpty, tc.exitsMidTurn, tc.savesDraft)
		}
		if !slices.Equal(c.InterruptKeys, tc.interrupt) {
			t.Errorf("%s: InterruptKeys = %v, want %v", tc.name, c.InterruptKeys, tc.interrupt)
		}
		for _, line := range []string{"submit", "clear", "interrupt"} {
			if strings.TrimSpace(c.Source[line]) == "" {
				t.Errorf("%s: no source cited for %s", tc.name, line)
			}
		}
		if !strings.Contains(c.Source["clear"], tc.version) {
			t.Errorf("%s: the clear source %q does not name the version %s it was measured on", tc.name, c.Source["clear"], tc.version)
		}
		if !strings.Contains(c.Source["interrupt"], tc.interruptSource) {
			t.Errorf("%s: the interrupt source %q should say %q", tc.name, c.Source["interrupt"], tc.interruptSource)
		}
		if tc.interruptSource == "measured" && strings.Contains(c.Source["interrupt"], "not measured") {
			t.Errorf("%s: the interrupt source %q says it was not measured", tc.name, c.Source["interrupt"])
		}
	}
}

// An adapter nobody measured says nothing, and so does an unknown runtime.
func TestUnmeasuredAdaptersHaveNoContract(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	for _, name := range []string{"forestage", "generic", "simulator", "unheard-of", ""} {
		c := r.ComposerFor(name)
		if c.Submit != "" || c.ClearKey != "" || len(c.InterruptKeys) != 0 || len(c.Source) != 0 {
			t.Errorf("%q has a contract it was never measured for: %+v", name, c)
		}
	}
}

// Both exit hazards are recorded wherever the clear key is C-c, so no caller
// can send one on a misread without the contract having said it ends the seat.
func TestEveryExitingClearKeyCarriesItsHazard(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"codex", "opencode"} {
		if c := NewRegistry().ComposerFor(name); c.ClearKey == "C-c" && !c.ClearExitsWhenEmpty {
			t.Errorf("%s: C-c is the clear key but the contract does not say it exits on an empty composer", name)
		}
	}
}
