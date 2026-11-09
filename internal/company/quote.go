package company

import (
	"fmt"
	"strconv"
	"unicode/utf8"
)

// maxFieldBytes caps one printed field, measured on the escaped text.
const maxFieldBytes = 2048

// quoteField is the one function every printed field goes through. A field
// made only of the characters of a name, a version or a path prints as it is,
// and any other field prints in Go's quoted form, so a control byte, a quote or
// a byte that is not UTF-8 reaches a message as an escape and never raw. always
// quotes a field that would print bare. The cap applies to the escaped text,
// since one byte can escape to four characters; a field cut by it ends with a
// plain "..." after its closing quote.
func quoteField(s string, always bool) string {
	if !always && isBare(s) && len(s) <= maxFieldBytes {
		return s
	}
	out := make([]byte, 0, min(len(s)+2, maxFieldBytes+3))
	out = append(out, '"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		var piece string
		if r == utf8.RuneError && size == 1 {
			piece = fmt.Sprintf(`\x%02x`, s[i])
		} else {
			q := strconv.Quote(string(r))
			piece = q[1 : len(q)-1]
		}
		if len(out)+len(piece)+1 > maxFieldBytes {
			return string(out) + `"...`
		}
		out = append(out, piece...)
		i += size
	}
	return string(append(out, '"'))
}

// isBare reports whether s is non-empty and made only of characters that need
// no quoting: letters, digits and . _ @ / : + - [ ] (the brackets let a key label such as args[0] print bare).
func isBare(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '_', c == '@', c == '/', c == ':', c == '+', c == '-', c == '[', c == ']':
		default:
			return false
		}
	}
	return true
}
