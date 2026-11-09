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
		{"the same deny as edit and read rules does not", "permission_path_rule_deny.json", "allow"},
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
	}
	// Under the fixtures that carry a deny on every permission, task x is that
	// deny; under the one that does not, no rule matches it.
	if got, res := LastMatch(bare, "task", "x"); res != NoRule {
		t.Errorf("bare deny: task x = %+v (%v), want NoRule", got, res)
	}
	for _, f := range []string{"permission_exact_glob_deny.json", "permission_path_rule_deny.json"} {
		if got, res := LastMatch(loadRules(t, f), "task", "x"); res != Decided || got.Action != "deny" || got.Permission != "*" {
			t.Errorf("%s: task x = %+v (%v), want the deny on *", f, got, res)
		}
	}
	if _, res := LastMatch(nil, "bash", "ls"); res != NoRule {
		t.Errorf("no rules = %v, want NoRule", res)
	}
}

// The matcher rows measured on opencode 1.18.34: `*` crosses `/`, `**` is the
// same as `*`, `?` is one character, a match is anchored at both ends and is
// case-sensitive, and the path is compared as given (a read path has no
// leading slash, so a rule written with one does not match). Each row is one
// rule against one target.
func TestLastMatchMeasuredMatcherRows(t *testing.T) {
	const b = "private/tmp/c11/work"
	for _, c := range []struct {
		name, pattern, target string
		want                  Result
	}{
		{"star crosses a slash", b + "/a/*", b + "/a/b/c", Decided},
		{"star on one segment", b + "/a/*", b + "/a/x", Decided},
		{"double star is star", b + "/a/**", b + "/a/b/c", Decided},
		{"question mark is one character", b + "/a/?", b + "/a/x", Decided},
		{"question mark is not two", b + "/a/?", b + "/a/xy", NoRule},
		{"a literal is anchored at the end", "a/x", b + "/a/x", NoRule},
		{"a leading star unanchors the start", "*a/x", b + "/za/x", Decided},
		{"a leading star and slash", "*/a/x", b + "/a/x", Decided},
		{"a rule with a leading slash does not match a path without one", "/" + b + "/a/x", b + "/a/x", NoRule},
		{"a rule without the slash matches an absolute path only as given", "/tmp/opencode/*", "/tmp/opencode/y", Decided},
		{"matching is case-sensitive, rule", b + "/A/*", b + "/a/x", NoRule},
		{"matching is case-sensitive, path", b + "/a/*", b + "/A/x", NoRule},
		{"a literal matches itself", "bash", "bash", Decided},
		{"star alone matches the empty string", "*", "", Decided},
	} {
		t.Run(c.name, func(t *testing.T) {
			rules := []Rule{{Permission: "*", Pattern: c.pattern, Action: "deny"}}
			if got, res := LastMatch(rules, "read", c.target); res != c.want {
				t.Fatalf("%q against %q = %+v (%v), want result %v", c.pattern, c.target, got, res, c.want)
			}
		})
	}
}

// Any form outside that table is reported as Unverified, never as allow, deny
// or no match, and it is reported even when an earlier rule would have decided,
// because a later rule may override it.
func TestLastMatchUnmeasuredSyntaxIsUnverified(t *testing.T) {
	for _, c := range []struct{ name, pattern, target string }{
		{"a character class", "/a/[xy]", "/a/x"},
		{"braces", "/a/{x,y}", "/a/x"},
		{"a negation", "!/a/x", "/a/x"},
		{"an escape", `/a/\*`, "/a/*"},
		{"a question mark against a non-ASCII target", "/a/?", "/a/\u00e9"},
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
	// Controls: an unmeasured pattern in a rule whose permission cannot match
	// is skipped, and the measured shapes beside it are decided.
	rules := []Rule{
		{Permission: "external_directory", Pattern: "/a/*", Action: "allow"},
		{Permission: "bash", Pattern: "{x,y}", Action: "deny"},
	}
	if got, res := LastMatch(rules, "external_directory", "/a/b"); res != Decided || got.Action != "allow" {
		t.Errorf("/a/* on /a/b = %+v (%v), want allow", got, res)
	}
	if _, res := LastMatch(rules, "external_directory", "/b/c"); res != NoRule {
		t.Errorf("/a/* on /b/c = %v, want NoRule", res)
	}
}
