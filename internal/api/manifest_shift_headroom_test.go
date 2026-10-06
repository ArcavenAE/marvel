package api

import (
	"fmt"
	"strings"
	"testing"
)

// headroomManifest is one role with a context-pressure arm. window and
// headroom are written as given; a zero window leaves runtime.context_window
// out, which is how a role leans on the model table.
func headroomManifest(window, headroom int) []byte {
	win := ""
	if window > 0 {
		win = fmt.Sprintf("\n    context_window = %d", window)
	}
	return fmt.Appendf(nil, `
[workspace]
name = "test"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "claude"%s

    [team.role.shift]
    on = "context-pressure"
    headroom_tokens = %d
`, win, headroom)
}

// A headroom that is not smaller than the window the role declares would
// fire at any occupancy. Refuse it at apply, where it is an error naming both
// numbers, instead of at runtime, where it is a seat shifting every tick.
func TestShiftHeadroomNotBelowDeclaredWindowIsRefused(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name             string
		window, headroom int
		refused          bool
	}{
		{"headroom above the window", 258_400, 400_000, true},
		{"headroom equal to the window", 258_400, 258_400, true},
		{"headroom one below the window", 258_400, 258_399, false},
		{"headroom well below the window", 1_000_000, 120_000, false},
		{"no declared window is not checked here", 0, 400_000, false},
	} {
		_, err := ParseManifestBytes(headroomManifest(tc.window, tc.headroom))
		if tc.refused {
			if err == nil {
				t.Errorf("%s: parsed, want a refusal", tc.name)
				continue
			}
			for _, want := range []string{"headroom_tokens", "400000", "context_window"} {
				if tc.headroom == 400_000 && !strings.Contains(err.Error(), want) {
					t.Errorf("%s: error %q should mention %q", tc.name, err, want)
				}
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: refused: %v", tc.name, err)
		}
	}
}

// A context-pressure arm inside an any-list is checked like a single one.
func TestShiftHeadroomCheckCoversAnArmInAnyList(t *testing.T) {
	t.Parallel()
	_, err := ParseManifestBytes([]byte(`
[workspace]
name = "test"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "claude"
    context_window = 200000

    [team.role.shift]
    any = [
      { on = "max-age", max_age = "24h" },
      { on = "context-pressure", headroom_tokens = 300000 },
    ]
`))
	if err == nil || !strings.Contains(err.Error(), "300000") {
		t.Errorf("err = %v, want the context-pressure arm refused", err)
	}
}

// With no declared window the headroom cannot be checked, which is said at
// apply as an advisory, one line per such role, and never as a refusal.
func TestShiftHeadroomAdvisoryWhenWindowUndeclared(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes(headroomManifest(0, 400_000))
	if err != nil {
		t.Fatal(err)
	}
	got := m.ShiftHeadroomAdvisories()
	if len(got) != 1 {
		t.Fatalf("advisories = %v, want exactly one", got)
	}
	for _, want := range []string{"squad", "worker", "400000", "context_window"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("advisory %q should mention %q", got[0], want)
		}
	}

	declared, err := ParseManifestBytes(headroomManifest(1_000_000, 120_000))
	if err != nil {
		t.Fatal(err)
	}
	if got := declared.ShiftHeadroomAdvisories(); len(got) != 0 {
		t.Errorf("a declared window needs no advisory, got %v", got)
	}
}

// A role with no context-pressure arm has nothing to check.
func TestShiftHeadroomAdvisorySilentWithoutAnArm(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(`
[workspace]
name = "test"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "claude"

    [team.role.shift]
    on = "max-age"
    max_age = "24h"
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.ShiftHeadroomAdvisories(); len(got) != 0 {
		t.Errorf("advisories = %v, want none", got)
	}
}
