package api

import (
	"fmt"
	"strings"
	"testing"
)

// A workspace, team, role, endpoint or policy name reaches file paths, bus
// subjects and a format scan. The parse refuses a name outside the address
// class the bus already uses: lower-case letters, digits and dashes, starting
// with a letter or digit, at most 32 bytes.
func TestParseManifestRefusesAnUnaddressableName(t *testing.T) {
	t.Parallel()
	build := func(ws, team, role, endpoint, policy string) string {
		return fmt.Sprintf(`
[workspace]
name = %q

[[policy]]
name = %q

[[team]]
name = %q

  [[team.role]]
  name = %q
  replicas = 1

    [team.role.runtime]
    command = "bash"

[[endpoint]]
name = %q
team = %q
`, ws, policy, team, role, endpoint, team)
	}
	long := strings.Repeat("a", 33)
	for _, tc := range []struct {
		kind string
		toml string
	}{
		{"workspace slash", build("a/b", "squad", "worker", "svc", "pol")},
		{"workspace dot-dot", build("..", "squad", "worker", "svc", "pol")},
		{"workspace upper", build("Proj", "squad", "worker", "svc", "pol")},
		{"team slash", build("proj", "../x", "worker", "svc", "pol")},
		{"team backslash", build("proj", `a\b`, "worker", "svc", "pol")},
		{"team space", build("proj", "a b", "worker", "svc", "pol")},
		{"team percent", build("proj", "a%d", "worker", "svc", "pol")},
		{"role dot", build("proj", "squad", "a.b", "svc", "pol")},
		{"role leading dash", build("proj", "squad", "-w", "svc", "pol")},
		{"endpoint slash", build("proj", "squad", "worker", "a/b", "pol")},
		{"policy slash", build("proj", "squad", "worker", "svc", "a/b")},
		{"team too long", build("proj", long, "worker", "svc", "pol")},
	} {
		if _, err := ParseManifestBytes([]byte(tc.toml)); err == nil {
			t.Errorf("%s: the manifest parsed, want a refusal", tc.kind)
		}
	}
	// Control: the same shape with addressable names parses, a 32-byte name is
	// the longest allowed, and digits may lead.
	for _, ok := range []string{
		build("proj", "squad", "worker", "svc", "pol"),
		build("aae", "arcaven", "marvel-builder", "agent-svc", "no-co-authored"),
		build("proj", strings.Repeat("a", 32), "9worker", "svc", "pol"),
	} {
		if _, err := ParseManifestBytes([]byte(ok)); err != nil {
			t.Errorf("an addressable manifest was refused: %v", err)
		}
	}
}
