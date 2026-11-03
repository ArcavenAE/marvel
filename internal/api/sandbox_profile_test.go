package api

import (
	"strings"
	"testing"
)

// sandbox_profile is a per-role key, in TOML and in YAML, carried to the applied
// role; absent means none. The key name was ratified by the operator on 2026-11-03.

func TestValidSandboxProfile(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"/p.sb", "/opt/marvel/examples/sandbox/goose-ollama.sb", "/a b/c.sb"} {
		if !ValidSandboxProfile(ok) {
			t.Errorf("ValidSandboxProfile(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "p.sb", "./p.sb", "~/p.sb", "/a/../b.sb", "/a//b.sb", "/a/./b.sb", "/a/", "/a\nb", "/a\rb", "/a\x00b", "/" + strings.Repeat("a", 1100)} {
		if ValidSandboxProfile(bad) {
			t.Errorf("ValidSandboxProfile(%q) = true, want false", bad)
		}
	}
}

func TestSandboxProfileIsARoleKeyCarriedToTheAppliedRole(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ extra, want string }{
		"absent": {"", ""},
		"set":    {`  sandbox_profile = "/opt/p.sb"`, "/opt/p.sb"},
	} {
		m, err := imageManifest(t, tc.extra)
		if err != nil {
			t.Fatalf("toml %s: %v", name, err)
		}
		if got := m.Teams[0].Roles[0].SandboxProfile; got != tc.want {
			t.Errorf("toml %s: sandbox_profile = %q, want %q", name, got, tc.want)
		}
		store := NewStore()
		if err := m.Apply(store); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		team, err := store.GetTeam("aae/squad")
		if err != nil {
			t.Fatal(err)
		}
		if got := team.Roles[0].SandboxProfile; got != tc.want {
			t.Errorf("applied %s: SandboxProfile = %q, want %q", name, got, tc.want)
		}
	}
	y := "workspace:\n  name: aae\nteams:\n  - name: squad\n    roles:\n      - name: w\n        replicas: 1\n        sandbox_profile: /opt/p.sb\n        runtime:\n          command: pi\n"
	m, err := ParseManifestBytes([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Teams[0].Roles[0].SandboxProfile; got != "/opt/p.sb" {
		t.Errorf("yaml: sandbox_profile = %q", got)
	}
}

// A path that is not a clean absolute path is refused at parse, naming the role
// and the key. Whether the file exists, and whether the host can use it, are
// launch-time questions and are not asked here.
func TestManifestRefusesASandboxProfileThatIsNotACleanAbsolutePath(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"p.sb", "~/p.sb", "/a/../b.sb", "/a//b.sb"} {
		_, err := imageManifest(t, `  sandbox_profile = "`+bad+`"`)
		if err == nil {
			t.Errorf("sandbox_profile %q parsed, want a refusal", bad)
			continue
		}
		if msg := err.Error(); !strings.Contains(msg, "sandbox_profile") || !strings.Contains(msg, `"w"`) {
			t.Errorf("sandbox_profile %q: error %q does not name the key and the role", bad, msg)
		}
	}
	if _, err := imageManifest(t, `  sandbox_profile = "/does/not/exist.sb"`); err != nil {
		t.Errorf("a path that does not exist was refused at parse: %v", err)
	}
}
