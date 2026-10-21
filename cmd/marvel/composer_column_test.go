package main

import (
	"slices"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// COMPOSER is registered, opt-in, and in no named set: reading it captures
// every seat's pane, so it appears only when the operator names it.
func TestComposerColumnIsOptIn(t *testing.T) {
	c, ok := lookupSessionColumn("composer")
	if !ok {
		t.Fatal("composer is not a registered column")
	}
	if c.header != "COMPOSER" {
		t.Errorf("header = %q, want COMPOSER", c.header)
	}
	if !optInColumns["composer"] {
		t.Error("composer is not opt-in, so the default table would read every pane")
	}
	if slices.Contains(wideSessionColumns, "composer") {
		t.Error("composer is in the wide set, so a wide terminal would read every pane")
	}
	if wantsComposer(defaultSessionColumns()) {
		t.Error("the default columns ask the daemon to read panes")
	}
	if !wantsComposer([]sessionColumn{c}) {
		t.Error("selecting composer does not ask the daemon for it")
	}
}

// The request asks for the column only when it is selected.
func TestGetRequestAsksForComposerOnlyWhenSelected(t *testing.T) {
	if _, ok := getRequest("sessions", false)["composer"]; ok {
		t.Error("a listing without the column asks the daemon to read panes")
	}
	if v, ok := getRequest("sessions", true)["composer"]; !ok || v != true {
		t.Errorf("composer = %v, %v; want true", v, ok)
	}
}

// The cell is the daemon's word, or "-" where no pane was read.
func TestComposerCell(t *testing.T) {
	for word, want := range map[string]string{"": "-", "mid_turn": "mid_turn", "unknown": "unknown", "holds_text": "holds_text"} {
		if got := newSessionRow(api.Session{Composer: word}).composer; got != want {
			t.Errorf("cell for %q = %q, want %q", word, got, want)
		}
	}
}
