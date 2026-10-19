package composer

import (
	"regexp"
	"strings"
)

// otherCSI matches a control sequence that is not an SGR one (cursor moves,
// erases). Its parameters and final byte are not text.
var otherCSI = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-ln-z]`)

// HasDim reports whether a capture taken with escape sequences draws any
// visible text dim (SGR 2). The attribute is read from the SGR parameters the
// way the claude reader reads it, so "2;37" is dim, "22" ends it and "38;5;2" is
// a colour. A dim attribute set around nothing visible does not count: a plain
// capture of that pane loses no text. Used by a plain capture to say it dropped
// the one attribute that separates a composer suggestion from a typed draft
// (marvel#941).
func HasDim(capture string) bool {
	capture = otherCSI.ReplaceAllString(capture, "")
	dim, at := false, 0
	for _, loc := range sgrSeq.FindAllStringIndex(capture, -1) {
		if dim && strings.TrimSpace(capture[at:loc[0]]) != "" {
			return true
		}
		dim = applySGR(dim, capture[loc[0]+2:loc[1]-1])
		at = loc[1]
	}
	return dim && strings.TrimSpace(capture[at:]) != ""
}
