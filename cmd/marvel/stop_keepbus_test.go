package main

import (
	"strings"
	"testing"
)

func intp(n int) *int { return &n }

// `marvel stop --keep-bus` does not refuse, and it says what the detach costs
// (aae-orc-4h9xp, section 6).
func TestKeepBusNoticeNamesTheDroppedCredentialsAndTheCommand(t *testing.T) {
	got := stopKeepBusNotice(intp(2))
	for _, want := range []string{"2 transient credential(s) will be dropped", "memory only", repushCommand} {
		if !strings.Contains(got, want) {
			t.Errorf("notice lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(strings.ToLower(got), "refus") {
		t.Errorf("the notice reads as a refusal:\n%s", got)
	}
}

func TestKeepBusNoticeIsQuietWhenNothingIsHeldAndHedgesForAnOlderDaemon(t *testing.T) {
	if got := stopKeepBusNotice(intp(0)); got != "" {
		t.Errorf("a daemon holding no credentials got a notice: %q", got)
	}
	got := stopKeepBusNotice(nil)
	if !strings.Contains(got, "any transient credentials") || !strings.Contains(got, repushCommand) {
		t.Errorf("an older daemon gives no count; the notice should say any may be dropped:\n%s", got)
	}
}
