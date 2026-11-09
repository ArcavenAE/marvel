package api

import (
	"reflect"
	"strings"
	"testing"
)

const gooseTOML = `
[workspace]
name = "acme"

[[team]]
name = "crew"

  [[team.role]]
  name = "worker"
  replicas = 1
  made_up_role_key = "kept lenient"

    [team.role.runtime]
    image = "goose"
    command = "goose"
    made_up_runtime_key = 1

    [team.role.runtime.goose]
%s
`

const gooseYAML = `
workspace:
  name: acme
teams:
  - name: crew
    roles:
      - name: worker
        replicas: 1
        made_up_role_key: kept lenient
        runtime:
          image: goose
          command: goose
          made_up_runtime_key: 1
          goose:
%s
`

func indent(s, pad string) string {
	lines := strings.Split(strings.Trim(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}

func parseGoose(t *testing.T, format, body string) (*Manifest, error) {
	t.Helper()
	if format == "toml" {
		return ParseManifestBytes([]byte(strings.Replace(gooseTOML, "%s", body, 1)))
	}
	return parseManifestYAML([]byte(strings.Replace(gooseYAML, "%s", indent(body, "            "), 1)))
}

func TestGooseTableParses(t *testing.T) {
	t.Parallel()
	bodies := map[string]string{
		"toml": `posture = "approve"
tools_allow = ["developer__shell"]
tools_deny = ["fetch"]
extensions = ["developer", "memory"]`,
		"yaml": `posture: approve
tools_allow: [developer__shell]
tools_deny: [fetch]
extensions: [developer, memory]`,
	}
	want := &GooseRuntime{
		Posture:    GoosePostureApprove,
		ToolsAllow: []string{"developer__shell"},
		ToolsDeny:  []string{"fetch"},
		Extensions: []string{"developer", "memory"},
	}
	for format, body := range bodies {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			m, err := parseGoose(t, format, body)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := m.Teams[0].Roles[0].Runtime.Goose; !reflect.DeepEqual(got, want) {
				t.Errorf("goose = %+v, want %+v", got, want)
			}
		})
	}
}

// An unknown key inside the goose table is refused; an unknown key anywhere
// else in the same manifest still applies, so the manifest stays lenient as a
// whole. The fixtures carry unknown role and runtime keys on purpose.
func TestGooseTableRefusesAnUnknownKeyOnlyInsideTheTable(t *testing.T) {
	t.Parallel()
	bodies := map[string]string{
		"toml": "posture = \"approve\"\ntool_allow = [\"x\"]",
		"yaml": "posture: approve\ntool_allow: [x]",
	}
	for format, body := range bodies {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			_, err := parseGoose(t, format, body)
			if err == nil {
				t.Fatal("an unknown goose key was accepted")
			}
			if !strings.Contains(err.Error(), `unknown key "tool_allow"`) || !strings.Contains(err.Error(), "posture, tools_allow, tools_deny, extensions") {
				t.Errorf("error %q does not name the key and the valid ones", err)
			}
		})
	}
}

func TestUnknownKeysOutsideTheGooseTableStillApply(t *testing.T) {
	t.Parallel()
	for format, body := range map[string]string{"toml": `posture = "auto"`, "yaml": `posture: auto`} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			m, err := parseGoose(t, format, body)
			if err != nil {
				t.Fatalf("made_up_role_key and made_up_runtime_key must stay lenient: %v", err)
			}
			if m.Teams[0].Roles[0].Runtime.Goose.Posture != GoosePostureAuto {
				t.Errorf("posture not read")
			}
		})
	}
}

func TestGoosePostureValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, toml string
		wantErr    string
	}{
		{"approve", `posture = "approve"`, ""},
		{"smart_approve", `posture = "smart_approve"`, ""},
		{"auto", `posture = "auto"`, ""},
		{"absent posture applies; the adapter refuses the launch", `extensions = ["developer"]`, ""},
		{"chat is never a posture", `posture = "chat"`, `posture "chat" is not valid`},
		{"wrong case", `posture = "Auto"`, `posture "Auto" is not valid`},
		{"blank tool entry", `posture = "approve"` + "\n" + `tools_deny = [" "]`, "tools_deny has an empty entry"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseGoose(t, "toml", tc.toml)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestGooseAdvisories(t *testing.T) {
	t.Parallel()
	role := func(name, image, command string, g *GooseRuntime) ManifestRole {
		return ManifestRole{Name: name, Replicas: 1, Runtime: ManifestRuntime{Image: image, Command: command, Goose: g}}
	}
	g := &GooseRuntime{Posture: GoosePostureApprove}
	m := &Manifest{Teams: []ManifestTeam{{Name: "crew", Roles: []ManifestRole{
		role("on-goose", "goose", "goose", g),
		role("on-claude", "claude", "claude", g),
		role("by-path", "", "/opt/bin/goose", g),
		role("no-table", "claude", "claude", nil),
	}}}}
	want := []string{
		`team crew role on-claude: runtime "claude" is not goose; runtime.goose is ignored`,
		`team crew role by-path: runtime "/opt/bin/goose" is not goose; runtime.goose is ignored`,
	}
	if got := m.GooseAdvisories(); !reflect.DeepEqual(got, want) {
		t.Errorf("advisories = %q, want %q", got, want)
	}
}

func TestStoreSnapshotDoesNotShareTheGooseLists(t *testing.T) {
	t.Parallel()
	r := Runtime{Goose: &GooseRuntime{ToolsDeny: []string{"a"}}}
	c := cloneRuntime(r)
	c.Goose.ToolsDeny[0] = "b"
	if r.Goose.ToolsDeny[0] != "a" {
		t.Error("a snapshot shares the goose list with the stored runtime")
	}
}

// A headless goose role with no prompt is refused at apply, in both formats.
// The refusal is scoped to goose: the same role on another adapter applies, and
// so does an interactive goose role (operator ruling on goose empty prompts).
func TestGooseHeadlessRoleNeedsAPromptAtApply(t *testing.T) {
	t.Parallel()
	tomlRole := func(image, extra string) string {
		return `
[workspace]
name = "acme"

[[team]]
name = "crew"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    image = "` + image + `"
    command = "` + image + `"
` + extra
	}
	yamlRole := func(image, extra string) string {
		return `
workspace:
  name: acme
teams:
  - name: crew
    roles:
      - name: worker
        replicas: 1
        runtime:
          image: ` + image + `
          command: ` + image + `
` + extra
	}
	tests := []struct {
		name    string
		image   string
		toml    string
		yaml    string
		wantErr bool
	}{
		{"headless, no prompt", "goose", `    mode = "headless"`, `          mode: headless`, true},
		{"headless, prompt", "goose", "    mode = \"headless\"\n    prompt = \"say ok\"", "          mode: headless\n          prompt: say ok", false},
		{"interactive, no prompt", "goose", ``, ``, false},
		{"headless on another adapter, no prompt", "claude", `    mode = "headless"`, `          mode: headless`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for format, body := range map[string]string{"toml": tomlRole(tc.image, tc.toml), "yaml": yamlRole(tc.image, tc.yaml)} {
				var err error
				if format == "toml" {
					_, err = ParseManifestBytes([]byte(body))
				} else {
					_, err = parseManifestYAML([]byte(body))
				}
				switch {
				case !tc.wantErr && err != nil:
					t.Errorf("%s: unexpected error: %v", format, err)
				case tc.wantErr && (err == nil || !strings.Contains(err.Error(), "runtime.prompt is required for a headless goose role")):
					t.Errorf("%s: error = %v, want the headless goose prompt refusal", format, err)
				}
			}
		})
	}
}
