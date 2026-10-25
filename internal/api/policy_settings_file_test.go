package api

import (
	"strings"
	"testing"
)

// A policy may name a JSON settings file instead of carrying inline settings.
// marvel reads it verbatim at projection and interprets nothing in it
// (ADR-005); a policy carries one or the other.

func settingsFileManifest(policy string) string {
	return `
[workspace]
name = "acme"

[[policy]]
name = "reviewer-contract"
` + policy + `
[[team]]
name = "squad"

  [[team.role]]
  name = "reviewer"
  replicas = 1
  policy = "reviewer-contract"

    [team.role.runtime]
    image = "claude"
    command = "claude"
`
}

func TestPolicyRefusesSettingsAndSettingsFile(t *testing.T) {
	t.Parallel()
	text := settingsFileManifest(`settings_file = "/etc/marvel/reviewer.json"

  [policy.settings.permissions]
  allow = ["Read"]
`)
	_, err := ParseManifestBytes([]byte(text))
	if err == nil {
		t.Fatal("a policy with settings and settings_file was accepted")
	}
	for _, want := range []string{"reviewer-contract", "settings", "settings_file"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}
}

func TestPolicySettingsFileMustBeAbsoluteOrHome(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"reviewer.json", "./reviewer.json", "../reviewer.json"} {
		text := settingsFileManifest(`settings_file = "` + path + `"`)
		_, err := ParseManifestBytes([]byte(text))
		if err == nil {
			t.Errorf("settings_file %q was accepted; a relative path would resolve against the daemon's directory", path)
			continue
		}
		if !strings.Contains(err.Error(), "reviewer-contract") || !strings.Contains(err.Error(), "settings_file") {
			t.Errorf("settings_file %q: error = %v, want it to name the policy and the key", path, err)
		}
	}
}

func TestPolicySettingsFileIsCarriedOnApply(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/etc/marvel/reviewer.json", "~/reviewer.json"} {
		m, err := ParseManifestBytes([]byte(settingsFileManifest(`settings_file = "` + path + `"`)))
		if err != nil {
			t.Fatalf("settings_file %q refused: %v", path, err)
		}
		store := NewStore()
		if err := m.Apply(store); err != nil {
			t.Fatalf("apply: %v", err)
		}
		p, err := store.GetPolicy("acme/reviewer-contract")
		if err != nil {
			t.Fatal(err)
		}
		if p.SettingsFile != path {
			t.Errorf("Policy.SettingsFile = %q, want %q", p.SettingsFile, path)
		}
		if len(p.Settings) != 0 {
			t.Errorf("Policy.Settings = %v, want none", p.Settings)
		}
	}
}

// Re-applying a policy that moves from inline settings to a file, or back,
// leaves only the form it now declares.
func TestPolicySwitchesBetweenInlineAndFile(t *testing.T) {
	t.Parallel()
	store := NewStore()
	apply := func(policy string) Policy {
		t.Helper()
		m, err := ParseManifestBytes([]byte(settingsFileManifest(policy)))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if err := m.Apply(store); err != nil {
			t.Fatalf("apply: %v", err)
		}
		p, err := store.GetPolicy("acme/reviewer-contract")
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	inline := `
  [policy.settings.permissions]
  allow = ["Read"]
`
	if p := apply(inline); p.SettingsFile != "" || len(p.Settings) == 0 {
		t.Fatalf("inline: SettingsFile=%q Settings=%v", p.SettingsFile, p.Settings)
	}
	if p := apply(`settings_file = "/etc/marvel/reviewer.json"`); p.SettingsFile == "" || len(p.Settings) != 0 {
		t.Fatalf("file: SettingsFile=%q Settings=%v, want the file and no inline settings", p.SettingsFile, p.Settings)
	}
	if p := apply(inline); p.SettingsFile != "" || len(p.Settings) == 0 {
		t.Fatalf("back to inline: SettingsFile=%q Settings=%v", p.SettingsFile, p.Settings)
	}
}
