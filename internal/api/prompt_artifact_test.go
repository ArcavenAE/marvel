package api

import (
	"strings"
	"testing"
)

// aae-orc-mdr0n, spec section 6: runtime.prompt_artifact_sha256 carries the
// role library's prompt-artifact hash. It is validated as 64 hex characters and
// passed through to the role's runtime, and a change to it is spec drift.

func promptArtifactManifest(value string) string {
	return `
[workspace]
name = "acme"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    image = "claude"
    command = "claude"
    prompt_artifact_sha256 = "` + value + `"
`
}

func TestPromptArtifactSha256IsPassedThroughToTheRole(t *testing.T) {
	want := strings.Repeat("0123456789abcdef", 4)
	m, err := ParseManifestBytes([]byte(promptArtifactManifest(want)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	team, err := store.GetTeam("acme/squad")
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	if got := team.Roles[0].Runtime.PromptArtifactSha256; got != want {
		t.Errorf("role runtime hash = %q, want %q", got, want)
	}
}

func TestPromptArtifactSha256IsLowercasedAtParse(t *testing.T) {
	m, err := ParseManifestBytes([]byte(promptArtifactManifest(strings.Repeat("AB", 32))))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := m.Teams[0].Roles[0].Runtime.PromptArtifactSha256; got != strings.Repeat("ab", 32) {
		t.Errorf("hash = %q, want lowercase", got)
	}
}

func TestPromptArtifactSha256RefusesAnythingButSixtyFourHexCharacters(t *testing.T) {
	for name, v := range map[string]string{
		"too short":     strings.Repeat("a", 63),
		"too long":      strings.Repeat("a", 65),
		"not hex":       strings.Repeat("g", 64),
		"a path":        "/etc/prompt.md",
		"with a prefix": "sha256:" + strings.Repeat("a", 64),
	} {
		_, err := ParseManifestBytes([]byte(promptArtifactManifest(v)))
		if err == nil {
			t.Errorf("%s: parsed, want a refusal", name)
			continue
		}
		if !strings.Contains(err.Error(), "prompt_artifact_sha256") {
			t.Errorf("%s: error %q does not name the field", name, err)
		}
		if strings.Contains(err.Error(), v) {
			t.Errorf("%s: error echoes the value: %q", name, err)
		}
	}
}

func TestPromptArtifactSha256UnsetIsAllowed(t *testing.T) {
	m, err := ParseManifestBytes([]byte(promptArtifactManifest("")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := m.Teams[0].Roles[0].Runtime.PromptArtifactSha256; got != "" {
		t.Errorf("hash = %q, want none", got)
	}
}
