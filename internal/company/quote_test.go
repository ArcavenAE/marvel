package company

import (
	"strings"
	"testing"
)

func TestQuoteFieldPrintsASafeFieldBareAndAnyOtherQuoted(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		in     string
		always bool
		want   string
	}{
		{"acme-roles", false, "acme-roles"},
		{"2026-11-01-ecc28ac", false, "2026-11-01-ecc28ac"},
		{"recipes/bus-seat.toml", false, "recipes/bus-seat.toml"},
		{"steady@1.0.0", false, "steady@1.0.0"},
		{"acme-roles", true, `"acme-roles"`},
		{"", false, `""`},
		{"a b", false, `"a b"`},
		{"a\nb", false, `"a\nb"`},
		{"a\x00b\x7f", false, `"a\x00b\x7f"`},
		{"tab\there", true, `"tab\there"`},
		{"bad\xffbyte", false, `"bad\xffbyte"`},
		{`say "hi"`, false, `"say \"hi\""`},
	} {
		if got := quoteField(c.in, c.always); got != c.want {
			t.Errorf("quoteField(%q, %v) = %s, want %s", c.in, c.always, got, c.want)
		}
	}
}

// No raw control byte reaches an output, whatever the input holds.
func TestQuoteFieldNeverEmitsARawControlByte(t *testing.T) {
	t.Parallel()
	var in strings.Builder
	for b := 0; b < 256; b++ {
		in.WriteByte(byte(b))
	}
	for _, always := range []bool{false, true} {
		out := quoteField(in.String(), always)
		for i := 0; i < len(out); i++ {
			if out[i] < 0x20 || out[i] == 0x7f {
				t.Fatalf("output holds raw control byte %#x at %d", out[i], i)
			}
		}
	}
}

// The cap is on the escaped output, since one byte can escape to four
// characters. A capped field ends with a plain "..." after its closing quote.
func TestQuoteFieldCapsTheEscapedOutputAt2KB(t *testing.T) {
	t.Parallel()
	for _, in := range []string{strings.Repeat("x", 10_000), strings.Repeat("\x01", 10_000), strings.Repeat("é", 10_000)} {
		out := quoteField(in, false)
		if len(out) > maxFieldBytes+len("...") {
			t.Errorf("output is %d bytes for a %d-byte input, want at most %d", len(out), len(in), maxFieldBytes+3)
		}
		if !strings.HasSuffix(out, `"...`) {
			t.Errorf("capped output ends %q, want a closing quote then ...", out[len(out)-8:])
		}
	}
	short := strings.Repeat("x", 100)
	if out := quoteField(short, true); strings.HasSuffix(out, "...") {
		t.Error("a field under the cap must not carry the marker")
	}
}
