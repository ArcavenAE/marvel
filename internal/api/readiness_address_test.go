package api

import "testing"

// The address matcher is spliced from the schema's agent_id pattern. The
// splice holds only for a pattern that is one expression between ^ and $: an
// alternation or an inner anchor would splice into a different expression
// that matches the wrong addresses. Such a pattern is refused, and each
// accepted pattern is held to the addresses it must and must not match.
func TestAgentAddressFromSplicesOnlyOneAnchoredExpression(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, pattern string
		match, refuse []string
	}{
		{
			name:    "the schema's class",
			pattern: "^[a-z0-9][a-z0-9-]{0,31}$",
			match:   []string{"agent://ops/rev-a", "agent://t1/a"},
			refuse:  []string{"agent://ops/*", "agent://ops/>", "agent://ops", "agent://ops/a/b", "agent://Ops/a", "xagent://ops/a"},
		},
		{
			name:    "a grouped alternation",
			pattern: "^(a|b)$",
			match:   []string{"agent://a/b", "agent://b/a"},
			refuse:  []string{"agent://a|b/a", "agent://ab/a", "b", "xb"},
		},
		{name: "a top-level alternation", pattern: "^a|b$"},
		{name: "an alternation of anchored branches", pattern: "^a$|^b$"},
		{name: "an inner anchor", pattern: "^a^b$"},
		{name: "a grouped inner anchor", pattern: "^(a|^b)$"},
		{name: "no end anchor", pattern: "^[a-z]+"},
		{name: "no start anchor", pattern: "[a-z]+$"},
		{name: "no anchors", pattern: "[a-z]+"},
		{name: "nothing between the anchors", pattern: "^$"},
		{name: "not a pattern", pattern: "^[a-z$"},
	} {
		re, err := agentAddressFrom(c.pattern)
		if c.match == nil {
			if err == nil {
				t.Errorf("%s: %q was accepted as %s, want refused", c.name, c.pattern, re)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %q refused: %v", c.name, c.pattern, err)
			continue
		}
		for _, a := range c.match {
			if !re.MatchString(a) {
				t.Errorf("%s: %s does not match %q", c.name, re, a)
			}
		}
		for _, a := range c.refuse {
			if re.MatchString(a) {
				t.Errorf("%s: %s matches %q", c.name, re, a)
			}
		}
	}
}
