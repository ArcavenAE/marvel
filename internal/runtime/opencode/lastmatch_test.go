package opencode

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const testData = "/scratch/seat/data"

// loadRules reads a fixture of rules in the shape `opencode debug agent`
// prints, with <data> replaced by the seat's data base.
func loadRules(t *testing.T, name string) []Rule {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Provenance string `json:"provenance"`
		Rules      []Rule `json:"rules"`
	}
	if err := json.Unmarshal([]byte(strings.ReplaceAll(string(raw), "<data>", testData)), &f); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if f.Provenance == "" || len(f.Rules) == 0 {
		t.Fatalf("%s: a fixture carries its provenance and its rules", name)
	}
	return f.Rules
}

// opencode appends an allow on <data>/opencode/tool-output/* after the user's
// rules unless the config denies exactly that glob (design fact 14), so a bare
// external_directory deny leaves the tool-output directory open and the
// exact-glob deny closes it. Each case has its opposite beside it.
func TestLastMatchToolOutputReappend(t *testing.T) {
	const target = testData + "/opencode/tool-output/x"
	cases := []struct {
		name, fixture string
		want          string
	}{
		{"a bare external_directory deny leaves tool-output allowed", "permission_bare_deny.json", "allow"},
		{"the exact-glob deny denies it", "permission_exact_glob_deny.json", "deny"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, res := LastMatch(loadRules(t, c.fixture), "external_directory", target)
			if res != Decided || got.Action != c.want {
				t.Fatalf("last match for external_directory %s = %+v (%v), want action %s", target, got, res, c.want)
			}
		})
	}
}

// Where one rule matches, first and last are the same rule, so these hold in
// either order; the paths under external_directory need the last match.
func TestLastMatchControls(t *testing.T) {
	bare := loadRules(t, "permission_bare_deny.json")
	for _, perm := range []string{"bash", "edit"} {
		if got, res := LastMatch(bare, perm, "x"); res != Decided || got.Action != "deny" || got.Permission != perm {
			t.Errorf("%s x = %+v (%v), want the one matching rule, deny", perm, got, res)
		}
	}
	for _, f := range []string{"permission_bare_deny.json", "permission_exact_glob_deny.json"} {
		rules := loadRules(t, f)
		if got, res := LastMatch(rules, "external_directory", "/etc/passwd"); res != Decided || got.Action != "deny" {
			t.Errorf("%s: external_directory /etc/passwd = %+v (%v), want deny", f, got, res)
		}
		if got, res := LastMatch(rules, "external_directory", "/tmp/opencode/y"); res != Decided || got.Action != "deny" {
			t.Errorf("%s: external_directory /tmp/opencode/y = %+v (%v), want the later deny to override the built-in allow", f, got, res)
		}
		if _, res := LastMatch(rules, "task", "x"); res != NoRule {
			t.Errorf("%s: task x = %v, want NoRule", f, res)
		}
	}
	if _, res := LastMatch(nil, "bash", "ls"); res != NoRule {
		t.Errorf("no rules = %v, want NoRule", res)
	}
}

// Glob syntax that no recorded run measures is reported as Unverified, never
// as allow, deny or no match, whichever rule it sits in.
func TestLastMatchUnmeasuredSyntaxIsUnverified(t *testing.T) {
	for _, c := range []struct{ name, pattern, target string }{
		{"a double star", "/a/**", "/a/b"},
		{"a question mark", "/a/?", "/a/b"},
		{"a star inside a segment", "/a/*.env", "/a/x.env"},
		{"a star in the middle", "/a/*/c", "/a/b/c"},
		{"a star crossing a slash", "/a/*", "/a/b/c"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rules := []Rule{
				{Permission: "external_directory", Pattern: "*", Action: "deny"},
				{Permission: "external_directory", Pattern: c.pattern, Action: "allow"},
			}
			if got, res := LastMatch(rules, "external_directory", c.target); res != Unverified {
				t.Fatalf("LastMatch = %+v (%v), want Unverified", got, res)
			}
		})
	}
	// Controls: the measured shapes are decided.
	rules := []Rule{{Permission: "external_directory", Pattern: "/a/*", Action: "allow"}}
	if got, res := LastMatch(rules, "external_directory", "/a/b"); res != Decided || got.Action != "allow" {
		t.Errorf("/a/* on /a/b = %+v (%v), want allow", got, res)
	}
	if _, res := LastMatch(rules, "external_directory", "/b/c"); res != NoRule {
		t.Errorf("/a/* on /b/c = %v, want NoRule", res)
	}
}
