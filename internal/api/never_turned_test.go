package api

import "testing"

func TestNeverTookATurn(t *testing.T) {
	one := 1
	zero := 0
	fresh := func() Session {
		var s Session
		s.ActiveTicks.Observable = true
		return s
	}
	for _, tc := range []struct {
		name string
		edit func(*Session)
		want bool
	}{
		{"observable and untouched", func(*Session) {}, true},
		{"zero spend counts as none", func(s *Session) { s.SpendOut = &zero; s.SpendPromptTokens = &zero }, true},
		{"no activity channel", func(s *Session) { s.ActiveTicks.Observable = false }, false},
		{"handed a first message", func(s *Session) { s.FirstPromptSha256 = "abc" }, false},
		{"context tokens", func(s *Session) { s.ContextTokens = 1 }, false},
		{"context requests", func(s *Session) { s.ContextRequests = 1 }, false},
		{"context percent", func(s *Session) { s.ContextPercent = 1 }, false},
		{"output spend", func(s *Session) { s.SpendOut = &one }, false},
		{"prompt spend", func(s *Session) { s.SpendPromptTokens = &one }, false},
	} {
		s := fresh()
		tc.edit(&s)
		if got := NeverTookATurn(s); got != tc.want {
			t.Errorf("%s: NeverTookATurn = %v, want %v", tc.name, got, tc.want)
		}
	}
}
