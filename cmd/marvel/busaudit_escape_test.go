package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/arcavenae/marvel/internal/bus"
)

// auditRunes are the runes strconv.IsPrint rejects that the design names.
// Fixtures write each by number, never as the raw rune.
var auditRunes = []rune{
	0x001B, 0x0085, 0x009B, 0x202A, 0x202B, 0x202C, 0x202D, 0x202E,
	0x2066, 0x2067, 0x2068, 0x2069, 0x200E, 0x200F, 0x061C, 0xE0001,
}

// jsonEscape writes r the way a JSON source text does: \uXXXX, or a surrogate
// pair above U+FFFF.
func jsonEscape(r rune) string {
	if r > 0xFFFF {
		hi, lo := utf16Pair(r)
		return fmt.Sprintf(`\u%04x\u%04x`, hi, lo)
	}
	return fmt.Sprintf(`\u%04x`, r)
}

func utf16Pair(r rune) (rune, rune) {
	r -= 0x10000
	return 0xD800 + (r>>10)&0x3FF, 0xDC00 + r&0x3FF
}

func rawIn(out []byte, r rune) bool { return bytes.Contains(out, []byte(string(r))) }

func envelopeOf(t *testing.T, out string) string {
	t.Helper()
	_, env, ok := strings.Cut(out, "envelope:\n")
	if !ok {
		t.Fatalf("no envelope block:\n%s", out)
	}
	return strings.TrimRight(env, "\n")
}

func auditWith(body string) bus.AuditRecord {
	rec := refusedRecord()
	rec.Body = []byte(body)
	return rec
}

func TestEscapedJSONBodyKeepsStructureInKeyValueAndNested(t *testing.T) {
	for _, r := range auditRunes {
		e := jsonEscape(r)
		src := `{"k` + e + `":"v` + e + `","n":{"b":"c` + e + `"},"z":1}`
		var orig any
		if err := json.Unmarshal([]byte(src), &orig); err != nil {
			t.Fatal(err)
		}
		wantNL := 0
		if indented, err := json.MarshalIndent(orig, "  ", "  "); err == nil {
			wantNL = bytes.Count(indented, []byte("\n"))
		}

		var line bytes.Buffer
		if err := printBusAudit(&line, auditWith(src), false); err != nil {
			t.Fatal(err)
		}
		env := envelopeOf(t, line.String())
		var js bytes.Buffer
		if err := printBusAudit(&js, auditWith(src), true); err != nil {
			t.Fatal(err)
		}
		for what, out := range map[string][]byte{"line output": line.Bytes(), "-o json": js.Bytes()} {
			if rawIn(out, r) {
				t.Errorf("U+%04X: %s holds the raw rune", r, what)
			}
			if !bytes.Contains(out, []byte(jsonEscape(r))) {
				t.Errorf("U+%04X: %s does not write it as %s", r, what, jsonEscape(r))
			}
		}
		if !json.Valid([]byte(env)) {
			t.Errorf("U+%04X: the envelope block is not valid JSON:\n%s", r, env)
			continue
		}
		var got any
		if err := json.Unmarshal([]byte(env), &got); err != nil || !reflect.DeepEqual(got, orig) {
			t.Errorf("U+%04X: the envelope decodes to %v (%v), want %v", r, got, err, orig)
		}
		if n := strings.Count(env, "\n"); n != wantNL {
			t.Errorf("U+%04X: envelope has %d newlines, json.Indent wrote %d", r, n, wantNL)
		}

		var whole struct {
			Envelope any `json:"envelope"`
		}
		if !json.Valid(js.Bytes()) {
			t.Errorf("U+%04X: -o json is not valid JSON:\n%s", r, js.String())
			continue
		}
		if err := json.Unmarshal(js.Bytes(), &whole); err != nil || !reflect.DeepEqual(whole.Envelope, orig) {
			t.Errorf("U+%04X: -o json envelope = %v (%v), want %v", r, whole.Envelope, err, orig)
		}
		var generic any
		_ = json.Unmarshal(js.Bytes(), &generic)
		re, _ := json.MarshalIndent(generic, "", "  ")
		if a, b := bytes.Count(bytes.TrimRight(js.Bytes(), "\n"), []byte("\n")), bytes.Count(re, []byte("\n")); a != b {
			t.Errorf("U+%04X: -o json has %d newlines, the indented form has %d", r, a, b)
		}
	}
}

func TestEscapedNonJSONBodyAndHeaders(t *testing.T) {
	for _, r := range auditRunes {
		rec := auditWith("pre" + string(r) + "post")
		rec.Refusal = "re" + string(r) + "fused"
		rec.Stage = "st" + string(r) + "age"
		rec.MsgID = "id" + string(r)
		for _, asJSON := range []bool{false, true} {
			var out bytes.Buffer
			if err := printBusAudit(&out, rec, asJSON); err != nil {
				t.Fatal(err)
			}
			if rawIn(out.Bytes(), r) {
				t.Errorf("U+%04X (json=%v): output holds the raw rune:\n%s", r, asJSON, out.String())
			}
			if asJSON && !json.Valid(out.Bytes()) {
				t.Errorf("U+%04X: -o json is not valid JSON:\n%s", r, out.String())
			}
		}
		var line bytes.Buffer
		_ = printBusAudit(&line, rec, false)
		// strconv.Quote writes ESC as \x1b and a rune above U+FFFF as \UXXXXXXXX;
		// every other rune in the set as \uXXXX.
		wants := []string{fmt.Sprintf(`\u%04x`, r)}
		switch {
		case r == 0x1B:
			wants = []string{`\x1b`, `\u001b`}
		case r > 0xFFFF:
			wants = []string{fmt.Sprintf(`\U%08x`, r), jsonEscape(r)}
		}
		found := false
		for _, w := range wants {
			if strings.Contains(line.String(), w) {
				found = true
			}
		}
		if !found {
			t.Errorf("U+%04X: line output has none of %v:\n%s", r, wants, line.String())
		}
	}
}

func TestEscapedHeadersPrintOnOneLine(t *testing.T) {
	rec := refusedRecord()
	rec.Refusal = "first\nsecond"
	rec.Stage = "a\nb"
	rec.MsgID = "x\ny"
	var out bytes.Buffer
	if err := printBusAudit(&out, rec, false); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"stage:", "refusal:", "msg_id:"} {
		lines := 0
		for _, l := range strings.Split(out.String(), "\n") {
			if strings.HasPrefix(l, key) {
				lines++
			}
		}
		if lines != 1 {
			t.Errorf("%s printed on %d lines", key, lines)
		}
	}
	// seq, time, subject, outcome, stage, refusal, msg_id, envelope:, then the
	// three lines of the indented body: a raw header newline would add one.
	if n := strings.Count(out.String(), "\n"); n != 11 {
		t.Errorf("output has %d lines, want 11:\n%q", n, out.String())
	}
}

func TestEscapeAllRewritesEveryRejectedRuneNewlineIncluded(t *testing.T) {
	if got, want := escapeAll("a\nb\x1bc"), `a\u000ab\u001bc`; got != want {
		t.Errorf("escapeAll = %q, want %q", got, want)
	}
	if got, want := escapeAll("plain text 1-2"), "plain text 1-2"; got != want {
		t.Errorf("escapeAll changed printable text: %q", got)
	}
	if got, want := escapeAll("x"+string(rune(0xE0001))), `x\udb40\udc01`; got != want {
		t.Errorf("escapeAll(U+E0001) = %q, want %q", got, want)
	}
}

func TestEscapeInStringsLeavesStructureAlone(t *testing.T) {
	in := "{\n  \"a\": \"x\\\"y\\\\\",\n  \"b\": [\n    \"q\u00a0\"\n  ]\n}"
	want := "{\n  \"a\": \"x\\\"y\\\\\",\n  \"b\": [\n    \"q\\u00a0\"\n  ]\n}"
	if got := escapeInStrings(in); got != want {
		t.Errorf("escapeInStrings:\n%q\nwant:\n%q", got, want)
	}
	// An escaped quote does not end the string, so what follows it is still inside.
	if got, want := escapeInStrings(`{"a":"\"`+string(rune(0x202E))+`"}`), `{"a":"\"\u202e"}`; got != want {
		t.Errorf("after an escaped quote: %q, want %q", got, want)
	}
}

func TestInvalidUTF8BecomesOneReplacementPerByte(t *testing.T) {
	cases := []struct {
		name, in string
		fffd     int
	}{
		{"a lone 0x9b", "x\x9by", 1},
		{"a truncated sequence", "x\xe2\x80y", 2},
	}
	for _, c := range cases {
		for name, fn := range map[string]func(string) string{
			"escapeInStrings": func(s string) string { return escapeInStrings(`{"a":"` + s + `"}`) },
			"escapeAll":       escapeAll,
		} {
			out := fn(c.in)
			if n := strings.Count(out, "\uFFFD"); n != c.fffd {
				t.Errorf("%s %s: %d replacement runes, want %d (%q)", name, c.name, n, c.fffd, out)
			}
			for _, b := range []byte{0x9b, 0xe2, 0x80} {
				if bytes.IndexByte([]byte(out), b) >= 0 {
					t.Errorf("%s %s: raw byte %#x left in %q", name, c.name, b, out)
				}
			}
			if !utf8.ValidString(out) {
				t.Errorf("%s %s: output is not valid UTF-8: %q", name, c.name, out)
			}
			if name == "escapeInStrings" && !json.Valid([]byte(out)) {
				t.Errorf("%s %s: output is not valid JSON: %q", name, c.name, out)
			}
		}
	}
}
