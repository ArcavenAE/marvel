package company

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The registry's size and its first entry, asserted first, so a row added or
// dropped is a decision made in this test and not a side effect.
func TestRegistryHoldsTheRowsOfThisLink(t *testing.T) {
	t.Parallel()
	if len(rows) != 40 {
		t.Fatalf("registry holds %d rows, want 40", len(rows))
	}
	if rows[0].id != "1" || rows[0].doc != `company <name>@<version>: <file>: unknown key "<key>"` {
		t.Fatalf("first row = %+v, want spec row 1", rows[0])
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if seen[r.id] {
			t.Errorf("row id %s appears twice", r.id)
		}
		seen[r.id] = true
	}
}

var slotRE = regexp.MustCompile(`<[^<>]+>`)

// A row's slots: how many fields it takes.
func slotCount(doc string) int { return len(slotRE.FindAllString(doc, -1)) }

// Every row prints each field it takes through the one quoting function: a
// field made of control bytes, quotes and a newline never reaches a message
// raw, whichever row it goes through.
func TestEveryRowQuotesItsFields(t *testing.T) {
	t.Parallel()
	const hostile = "a\nb\x01\"c"
	for _, r := range rows {
		fields := make([]string, slotCount(r.doc))
		for i := range fields {
			fields[i] = hostile
		}
		got := refuse(r.id, fields...)
		if got == nil || got.Row != r.id || got.Message == "" {
			t.Errorf("row %s: refuse = %+v, want a message", r.id, got)
			continue
		}
		for i := 0; i < len(got.Message); i++ {
			if c := got.Message[i]; c < 0x20 || c == 0x7f {
				t.Errorf("row %s: message holds raw control byte %#x: %q", r.id, c, got.Message)
				break
			}
		}
		if strings.Contains(got.Message, "a\nb") {
			t.Errorf("row %s: message %q printed a hostile field raw", r.id, got.Message)
		}
	}
}

// Every row of the registry is in the spec's refusal table with the same text,
// and the table's size is asserted first.
func TestEveryRowHasADocRow(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("../../docs/design/company-recipe-schema.md")
	if err != nil {
		t.Fatal(err)
	}
	_, table, ok := strings.Cut(string(b), "## Refusal messages")
	if !ok {
		t.Fatal("the spec has no Refusal messages section")
	}
	table, _, _ = strings.Cut(table, "\n## ")
	var docs []string
	for _, line := range strings.Split(table, "\n") {
		if !strings.HasPrefix(line, "| ") || strings.HasPrefix(line, "| case ") || strings.HasPrefix(line, "|---") {
			continue
		}
		cells := strings.SplitN(line, "|", 4)
		if len(cells) < 3 {
			continue
		}
		msg := strings.TrimSpace(cells[2])
		msg = strings.TrimSuffix(strings.TrimPrefix(msg, "`"), "`")
		docs = append(docs, msg)
	}
	if len(docs) != 42 {
		t.Fatalf("the spec's refusal table holds %d rows, want 42 (23 spec rows, the 12 rows of the decoders and the 7 rows of the resolver)", len(docs))
	}
	have := map[string]bool{}
	for _, d := range docs {
		have[d] = true
	}
	for _, r := range rows {
		if !have[r.doc] {
			t.Errorf("row %s: %q is not in the spec's refusal table", r.id, r.doc)
		}
	}
}

// A directory name with a newline is printed escaped, never raw.
func TestRootPathWithNewlineIsQuoted(t *testing.T) {
	dir := Ident{Name: "acme-roles", Version: "2026-11-01-ecc28ac"}
	odd := "acme\nroles"
	err := decodeCompanyRefused(t, Ident{Name: odd, Version: dir.Version}, goodDefs)
	r := refusalOf(t, err)
	if strings.ContainsAny(r.Message, "\n\x00") || !strings.Contains(r.Message, `"acme\nroles"`) {
		t.Fatalf("message %q, want the directory printed as \"acme\\nroles\"", r.Message)
	}
}

// An unknown key can be anything the file's author typed. It is escaped, and
// it is bounded.
func TestUnknownKeyIsQuotedAndBounded(t *testing.T) {
	key := strings.Repeat("k", 5000) + `\nz`
	doc := goodDefs + `"` + key + `" = 1` + "\n"
	err := decodeCompanyRefused(t, defsDir, doc)
	r := refusalOf(t, err)
	if r.Row != "1" {
		t.Fatalf("row = %s, want 1", r.Row)
	}
	if len(r.Message) > 2*maxFieldBytes {
		t.Errorf("message is %d bytes, want it bounded", len(r.Message))
	}
	if strings.ContainsAny(r.Message, "\n\x00") {
		t.Error("message holds a raw control byte")
	}
	if !strings.Contains(r.Message, `"...`) {
		t.Error("message does not show that the key was cut")
	}
}
