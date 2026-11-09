package panestate

import (
	"strings"
	"testing"
)

func devChannelsSet(t *testing.T) ([]Pattern, Pattern, string) {
	t.Helper()
	sets, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("load embedded: %v", err)
	}
	for _, p := range sets {
		if p.ID == "dev-channels-consent" {
			raw, err := EmbeddedSample(p)
			if err != nil {
				t.Fatal(err)
			}
			return sets, p, raw
		}
	}
	t.Fatal("no shipped dev-channels-consent pattern")
	return nil, Pattern{}, ""
}

// The prompt a seat shows at startup under a development channel reads parked
// for a reason no listed one fits, at 2.1.310 only.
func TestDevChannelsPromptReadsParkedOther(t *testing.T) {
	sets, p, raw := devChannelsSet(t)
	if p.Harness != "claude" || p.HarnessVersion != "2.1.310" || p.MinVersion != "2.1.310" || p.MaxVersion != "2.1.310" {
		t.Fatalf("pattern scope = %s %s [%s,%s], want claude 2.1.310 only", p.Harness, p.HarnessVersion, p.MinVersion, p.MaxVersion)
	}
	r := Classify(sets, "claude", "2.1.310", rows(raw))
	if r.State != StateParked || r.Confidence != ConfHigh || r.Reason != "other" {
		t.Fatalf("result = %+v, want parked/high/other", r)
	}
	for _, v := range []string{"2.1.309", "2.1.311", "2.2.0"} {
		if r := Classify(sets, "claude", v, rows(raw)); r.State != StateUnknown {
			t.Errorf("claude %s: %+v, want unknown", v, r)
		}
	}
}

// Another channel name changes nothing: the rows start at option 1, and the
// evidence carries no cursor text or screen text beyond the fixed rows.
func TestDevChannelsPromptIgnoresTheChannelLineAndMasksTheCursor(t *testing.T) {
	sets, _, raw := devChannelsSet(t)
	other := strings.ReplaceAll(raw, "server:director", "server:other")
	r := Classify(sets, "claude", "2.1.310", rows("chat above\n\n"+other))
	if r.State != StateParked {
		t.Fatalf("another channel name: %+v, want parked", r)
	}
	for _, e := range r.Evidence {
		if strings.Contains(e, "❯") {
			t.Fatalf("evidence carries the cursor: %q", e)
		}
	}
	if !strings.Contains(strings.Join(r.Evidence, "\n"), "<masked>") {
		t.Fatalf("evidence = %q, want the cursor column masked", r.Evidence)
	}
}

// An answered prompt that scrolled up, and a prompt quoted in chat, are not a park.
func TestDevChannelsPromptAnsweredOrQuotedIsNotParked(t *testing.T) {
	sets, _, raw := devChannelsSet(t)
	if r := Classify(sets, "claude", "2.1.310", rows(raw+"\n  ● working\n")); r.State == StateParked {
		t.Fatalf("parked from a prompt with output below it: %+v", r)
	}
	quoted := strings.ReplaceAll(raw, "  ❯ 1.", "  >❯ 1.")
	if r := Classify(sets, "claude", "2.1.310", rows(quoted)); r.State == StateParked {
		t.Fatalf("parked from a quoted copy: %+v", r)
	}
	for _, edit := range [][2]string{
		{"Enter to confirm", "Enter to continue"},
		{"2. Exit", "2. Quit"},
		{"I am using this for local development", "I am using this for development"},
	} {
		if r := Classify(sets, "claude", "2.1.310", rows(strings.Replace(raw, edit[0], edit[1], 1))); r.State == StateParked {
			t.Errorf("parked after editing %q: %+v", edit[0], r)
		}
	}
}
