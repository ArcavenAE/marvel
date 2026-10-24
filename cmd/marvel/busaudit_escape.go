package main

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The audit record's text comes from a stranger's send, so no rune that
// strconv.IsPrint rejects may reach the terminal raw. encoding/json escapes
// the C0 controls and U+2028 and U+2029 but passes C1 controls and the bidi
// controls through; these two passes close that gap.

// writeEscapedRune writes r as \uXXXX, or as a UTF-16 surrogate pair above
// U+FFFF.
func writeEscapedRune(b *strings.Builder, r rune) {
	if r > 0xFFFF {
		r -= 0x10000
		fmt.Fprintf(b, `\u%04x\u%04x`, 0xD800+(r>>10)&0x3FF, 0xDC00+r&0x3FF)
		return
	}
	fmt.Fprintf(b, `\u%04x`, r)
}

// escapeInStrings rewrites, inside the string literals of JSON text only,
// each rune strconv.IsPrint rejects. Outside a string every byte is copied, so
// the newlines and spaces between tokens survive. Inside a string a
// backslash and the byte after it are copied as they are, and an unescaped
// quote ends the string. An invalid UTF-8 byte becomes U+FFFD, one for each.
func escapeInStrings(s string) string {
	var b strings.Builder
	inString := false
	for i := 0; i < len(s); {
		if !inString {
			b.WriteByte(s[i])
			if s[i] == '"' {
				inString = true
			}
			i++
			continue
		}
		switch s[i] {
		case '\\':
			b.WriteByte(s[i])
			i++
			if i < len(s) {
				b.WriteByte(s[i])
				i++
			}
			continue
		case '"':
			inString = false
			b.WriteByte(s[i])
			i++
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			b.WriteRune(utf8.RuneError)
		case !strconv.IsPrint(r):
			writeEscapedRune(&b, r)
		default:
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	return b.String()
}

// escapeAll rewrites every rune strconv.IsPrint rejects in plain text, a
// newline included, so the text prints on one line. An invalid UTF-8 byte
// becomes U+FFFD, one for each.
func escapeAll(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			b.WriteRune(utf8.RuneError)
		case !strconv.IsPrint(r):
			writeEscapedRune(&b, r)
		default:
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	return b.String()
}
