package api

import (
	"strings"
	"testing"
)

// handoff_via = "tool" on the shift block (docs/design/read-only-seat-handoff.md
// section 6): the role's seat hands off through the submit_handoff tool, and
// marvel writes the declared file for it. The key is written in TOML and YAML
// because the two decoders drop unknown keys differently.

const (
	viaMaxAgeTOML = "    on = \"max-age\"\n    max_age = \"72h\"\n"
	viaMaxAgeYAML = "          on: max-age\n          max_age: 72h\n"
)

func TestHandoffViaToolParsesOnTheShiftBlock(t *testing.T) {
	t.Parallel()
	for name, src := range map[string]string{
		"toml": shiftTOML("", viaMaxAgeTOML+pairTOML+"    handoff_via = \"tool\"\n"),
		"yaml": shiftYAML("", viaMaxAgeYAML+pairYAML+"          handoff_via: tool\n"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := applyShift(t, src)
			if p == nil || p.HandoffVia != ShiftHandoffViaTool {
				t.Fatalf("HandoffVia = %+v, want %q", p, ShiftHandoffViaTool)
			}
			if p.Handoff == "" || p.HandoffMarker == "" {
				t.Errorf("the declared file and marker were dropped: %+v", p)
			}
		})
	}
}

func TestHandoffViaUnsetStaysEmpty(t *testing.T) {
	t.Parallel()
	p := applyShift(t, shiftTOML("", viaMaxAgeTOML+pairTOML))
	if p == nil || p.HandoffVia != "" {
		t.Fatalf("HandoffVia = %+v, want empty", p)
	}
}

// Only "tool" is a way to hand off other than the file the seat writes, and a
// declaration that is not it is refused rather than read as the default.
func TestHandoffViaRefusesAnyValueButTool(t *testing.T) {
	t.Parallel()
	for name, src := range map[string]string{
		"toml": shiftTOML("", viaMaxAgeTOML+pairTOML+"    handoff_via = \"pipe\"\n"),
		"yaml": shiftYAML("", viaMaxAgeYAML+pairYAML+"          handoff_via: pipe\n"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := applyShiftErr(t, src)
			if err == nil || !strings.Contains(err.Error(), "handoff_via") || !strings.Contains(err.Error(), `"tool"`) {
				t.Fatalf("err = %v, want it to name handoff_via and the one valid value", err)
			}
		})
	}
}

// marvel writes the declared file for a tool handoff, so a role that names the
// tool and no file has nothing for the controller to watch.
func TestHandoffViaToolNeedsTheDeclaredFile(t *testing.T) {
	t.Parallel()
	err := applyShiftErr(t, shiftTOML("", viaMaxAgeTOML+"    handoff_via = \"tool\"\n"))
	if err == nil || !strings.Contains(err.Error(), "handoff_via") || !strings.Contains(err.Error(), "handoff_marker") {
		t.Fatalf("err = %v, want it to say handoff_via needs handoff and handoff_marker", err)
	}
}

// A misspelled key stays an error: the key list is what made handoff_via
// refusable in the first place.
func TestHandoffViaTypoIsRefused(t *testing.T) {
	t.Parallel()
	_, err := ParseManifestBytes([]byte(shiftTOML("", viaMaxAgeTOML+pairTOML+"    handoff_vai = \"tool\"\n")))
	if err == nil {
		t.Fatal("handoff_vai was accepted")
	}
	if err := applyShiftErr(t, shiftTOML("", viaMaxAgeTOML+pairTOML+"    handoff_vai = \"tool\"\n")); err == nil || !strings.Contains(err.Error(), "handoff_vai") {
		t.Fatalf("err = %v, want it to name handoff_vai", err)
	}
}

func applyShiftErr(t *testing.T, src string) error {
	t.Helper()
	m, err := ParseManifestBytes([]byte(src))
	if err != nil {
		return err
	}
	return m.Apply(NewStore())
}

// HandoffViaRefusals lists a role that asks for the tool on a harness no
// seeder can offer it on, naming the team, the role and the runtime.
func TestHandoffViaRefusalsNameTheRoleAndRuntime(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(shiftTOML("", viaMaxAgeTOML+pairTOML+"    handoff_via = \"tool\"\n")))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.HandoffViaRefusals(func(ManifestRole) bool { return true }); len(got) != 0 {
		t.Errorf("refusals = %q, want none when the harness can offer the tool", got)
	}
	got := m.HandoffViaRefusals(func(ManifestRole) bool { return false })
	if len(got) != 1 {
		t.Fatalf("refusals = %q, want one", got)
	}
	for _, w := range []string{"team squad", "role worker", `"claude"`, "handoff_via"} {
		if !strings.Contains(got[0], w) {
			t.Errorf("refusal %q does not contain %q", got[0], w)
		}
	}
	if got := m.HandoffViaRefusals(nil); got != nil {
		t.Errorf("refusals with no capability = %q, want none", got)
	}
}

// A role that does not ask for the tool is never refused, whatever its harness.
func TestHandoffViaRefusalsIgnoreARoleThatDoesNotAskForTheTool(t *testing.T) {
	t.Parallel()
	m, err := ParseManifestBytes([]byte(shiftTOML("", viaMaxAgeTOML+pairTOML)))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.HandoffViaRefusals(func(ManifestRole) bool { return false }); len(got) != 0 {
		t.Errorf("refusals = %q, want none", got)
	}
}
