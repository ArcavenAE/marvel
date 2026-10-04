package runtime

import "testing"

func TestClaudeForegroundRule(t *testing.T) {
	r := NewRegistry()
	rule, ok := r.Resolve("claude").(ForegroundRule)
	if !ok {
		t.Fatal("claude has no foreground rule")
	}
	cases := []struct {
		cmd, ver string
		in       bool
	}{
		{"2.1.285", "2.1.285", true},
		{"2.1.283", "2.1.283", true},
		{"claude", "", true},
		{"less", "", false},
		{"zsh", "", false},
		{"2.1", "", false},
		{"2.1.285-beta", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		ver, in := rule.Foreground(c.cmd)
		if in != c.in || ver != c.ver {
			t.Errorf("Foreground(%q) = %q, %v; want %q, %v", c.cmd, ver, in, c.ver, c.in)
		}
	}
}

// An adapter with no rule is never a candidate.
func TestOnlyClaudeCarriesAForegroundRule(t *testing.T) {
	r := NewRegistry()
	for _, name := range []string{"forestage", "codex", "opencode", "simulator", "generic", "no-such-runtime"} {
		if _, ok := r.Resolve(name).(ForegroundRule); ok {
			t.Errorf("%s carries a foreground rule", name)
		}
	}
}
