package main

import (
	"fmt"
	"regexp"
	"strings"
)

// injectStep is one send-keys request the inject command will issue. A bare
// wake is one step; text followed by a control key is two.
type injectStep struct {
	Text    string
	Literal bool
	Enter   bool
}

// namedTmuxKeys are the key names a person is most likely to type as if they
// were an instruction rather than text. The list is not tmux's full table: it
// is the set worth refusing, which is the set whose literal form does damage.
var namedTmuxKeys = map[string]struct{}{
	"Enter": {}, "Escape": {}, "Tab": {}, "Space": {}, "BSpace": {},
	"BTab": {}, "DC": {}, "IC": {}, "Up": {}, "Down": {}, "Left": {},
	"Right": {}, "Home": {}, "End": {}, "PageUp": {}, "PageDown": {},
	"PPage": {}, "NPage": {},
}

// modifierKey matches tmux's modifier forms (C-c, M-x, S-Tab) and the
// function keys, which are patterns rather than a fixed list.
var modifierKey = regexp.MustCompile(`^([CMS]-[^-\s]+|F([1-9]|1[0-2]))$`)

// looksLikeTmuxKey reports whether text is exactly a tmux key name. Exactness
// is the whole point: "Enter" is refused, "Enter your name" is ordinary text
// and passes untouched.
func looksLikeTmuxKey(text string) bool {
	if _, ok := namedTmuxKeys[text]; ok {
		return true
	}
	return modifierKey.MatchString(text)
}

// resolveInject turns the command line into the steps to send.
//
// The defect it exists to close (marvel#341): inject defaults to literal, so
// `inject <key> Enter` typed the five characters "Enter" into the composer. On
// a Claude draft that appends a word; on a codex approval menu it matches
// nothing, cancels the pending command, and returns the seat to a clean idle
// prompt that reads healthy. The seat records a refusal the agent never made,
// and the control plane shows nothing wrong.
//
// literalSet is whether --literal was given explicitly. The trap is the
// DEFAULT, not the flag, so an operator who states literal intent is believed
// and only the unstated case is refused. That keeps the guard from breaking a
// caller who really does want the word.
func resolveInject(text string, hasText bool, keys []string, literal, enter, literalSet bool) ([]injectStep, error) {
	if !hasText && len(keys) == 0 {
		return nil, fmt.Errorf("nothing to send: give text, or --key for a control key such as --key Enter")
	}
	if len(keys) > 0 && enter {
		return nil, fmt.Errorf("--enter and --key both append a keystroke; use --key Enter alone")
	}
	for _, k := range keys {
		if strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("--key needs a tmux key name, for example Enter, Escape or C-c")
		}
	}
	if hasText && len(keys) == 0 && literal && !literalSet && looksLikeTmuxKey(text) {
		return nil, fmt.Errorf(
			"%q is a tmux key name and inject sends text literally by default, "+
				"so this would type the characters %q rather than press the key.\n"+
				"  press the key:        marvel inject <session-key> --key %s\n"+
				"  submit a draft:       marvel inject <session-key> '' --enter\n"+
				"  really send the word: marvel inject <session-key> %q --literal",
			text, text, text, text)
	}

	var steps []injectStep
	if hasText {
		steps = append(steps, injectStep{Text: text, Literal: literal, Enter: enter && len(keys) == 0})
	}
	for _, k := range keys {
		steps = append(steps, injectStep{Text: k, Literal: false})
	}
	return steps, nil
}

// describeInject is the line printed on success.
func describeInject(sessionKey string, steps []injectStep) string {
	var bytes int
	var keys []string
	for _, s := range steps {
		if s.Literal {
			bytes += len(s.Text)
			if s.Enter {
				keys = append(keys, "Enter")
			}
			continue
		}
		keys = append(keys, s.Text)
	}
	switch {
	case bytes > 0 && len(keys) > 0:
		return fmt.Sprintf("injected %d bytes and %s into %s", bytes, strings.Join(keys, " "), sessionKey)
	case len(keys) > 0:
		return fmt.Sprintf("injected %s into %s", strings.Join(keys, " "), sessionKey)
	default:
		return fmt.Sprintf("injected %d bytes into %s", bytes, sessionKey)
	}
}
