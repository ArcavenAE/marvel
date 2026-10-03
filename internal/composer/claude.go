package composer

import (
	"regexp"
	"strings"
)

// claudeReader reads Claude Code's composer from a capture taken with escape
// sequences (tmux capture-pane -p -e). Its shapes come from real captures of v2.1.288
// (internal/composer/testdata/claude), and it matches shapes, not words, because
// the spinner verb and the done line's text vary from run to run.
//
// What the captures show:
//   - The live composer is a prompt line "❯" followed by a no-break space,
//     between two rules, with continuation lines indented two spaces. The echo
//     of an earlier prompt in the history has a plain space after the glyph.
//   - A fresh composer shows a dim (SGR 2) placeholder, 'Try "..."', which is
//     not a draft. A plain capture cannot tell it from typed text, so the shape
//     alone reads Unknown.
//   - While the model thinks, a spinner line sits above the composer. While text
//     streams there is no spinner and the composer is empty, so an empty
//     composer is not evidence of idle: it is Empty only beside a finished turn's
//     done line or the placeholder, and anything else reads Unknown.
//
// It has no clear key until aae-orc-g88i1 grounds one.
type claudeReader struct{}

func (claudeReader) Name() string     { return "claude" }
func (claudeReader) Escapes() bool    { return true }
func (claudeReader) Preflight() bool  { return false }
func (claudeReader) ClearKey() string { return "" }

const (
	claudePrompt = "❯ "
	// claudeHintIndent is how far right the hint lines above the composer
	// ("auto mode unavailable", "ctrl+g to edit in VS Code") are pushed.
	claudeHintIndent = 20
)

var (
	ansiSeq  = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	ruleLine = regexp.MustCompile(`^─{10,}$`)
	// The spinner and done lines are matched by their own glyphs, at the margin.
	// A reply's text is model-controlled and starts with the reply bullet (or is
	// indented), so a reply that imitates either line must not read as the
	// harness's own marker: that would turn an unsent submit into "confirmed".
	spinnerLine = regexp.MustCompile(`^[·✢✳✶✻✽]\s+\S+…\s+\(\d`)
	doneLine    = regexp.MustCompile(`^✻\s+\S+ for (?:\d+[hms]\s*)+· done\b`)
	placeholder = regexp.MustCompile(`^Try ".*"$`)
)

func (claudeReader) Read(capture string) State {
	raw := strings.Split(capture, "\n")
	clean := make([]string, len(raw))
	for i, l := range raw {
		clean[i] = strings.TrimRight(ansiSeq.ReplaceAllString(l, ""), " ")
	}

	// The composer: the last prompt line that sits under a rule and above one.
	top := -1
	for i := len(clean) - 1; i > 0; i-- {
		if strings.HasPrefix(clean[i], claudePrompt) && ruleLine.MatchString(clean[i-1]) {
			top = i
			break
		}
	}
	if top < 0 {
		return Unknown
	}
	draft := []string{strings.TrimPrefix(clean[top], claudePrompt)}
	closed := false
	for j := top + 1; j < len(clean); j++ {
		if ruleLine.MatchString(clean[j]) {
			closed = true
			break
		}
		if !strings.HasPrefix(clean[j], "  ") {
			break
		}
		draft = append(draft, strings.TrimPrefix(clean[j], "  "))
	}
	if !closed {
		return Unknown
	}
	text := strings.TrimSpace(strings.Join(draft, "\n"))

	// What the turn is doing: the first line above the composer that is not a
	// blank or a right-aligned hint.
	above := ""
	for i := top - 2; i >= 0; i-- {
		l := clean[i]
		if strings.TrimSpace(l) == "" || len(l)-len(strings.TrimLeft(l, " ")) >= claudeHintIndent {
			continue
		}
		above = l
		break
	}
	if spinnerLine.MatchString(above) {
		return MidTurn
	}

	if text != "" {
		// The placeholder is the only dim text in a composer. Dim is read from the
		// SGR parameters (so "2;37" is dim, "22" ends it, and "38;5;2" is a colour,
		// not dim). A placeholder under spinner-less streaming text reads Empty:
		// that relies on claude never drawing the placeholder mid-turn, which held
		// in every real capture reviewed and is not a guarantee.
		dim := dimAtText(raw[top]) && len(draft) == 1
		if placeholder.MatchString(text) {
			if dim {
				return Empty
			}
			if !strings.Contains(capture, "\x1b") {
				// A plain capture cannot say whether this is the placeholder or
				// a message someone typed in its shape.
				return Unknown
			}
		}
		return HoldsText
	}
	if doneLine.MatchString(above) {
		return Empty
	}
	return Unknown
}

// dimAtText reports whether the first visible character after the composer's
// prompt is drawn dim (SGR 2), reading the escape sequences that precede it.
func dimAtText(line string) bool {
	i := strings.Index(line, claudePrompt)
	if i < 0 {
		return false
	}
	rest := line[i+len(claudePrompt):]
	dim := false
	for len(rest) > 0 {
		loc := sgrSeq.FindStringIndex(rest)
		if loc == nil || loc[0] != 0 {
			if loc != nil && strings.TrimLeft(rest[:loc[0]], " ") == "" {
				rest = rest[loc[0]:]
				continue
			}
			return dim
		}
		dim = applySGR(dim, rest[2:loc[1]-1])
		rest = rest[loc[1]:]
	}
	return dim
}

var sgrSeq = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// applySGR returns the dim state after one SGR sequence's parameters.
func applySGR(dim bool, params string) bool {
	if params == "" {
		return false
	}
	p := strings.Split(params, ";")
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case "0", "":
			dim = false
		case "2":
			dim = true
		case "22":
			dim = false
		case "38", "48", "58":
			// An extended colour: 5;n or 2;r;g;b. Its numbers are not attributes.
			if i+1 < len(p) && p[i+1] == "5" {
				i += 2
			} else if i+1 < len(p) && p[i+1] == "2" {
				i += 4
			}
		}
	}
	return dim
}
