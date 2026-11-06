package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// svcSession is a session on a harness with the backend fields, credential
// source, account home and declared login label the SERVICE cell reads.
func svcSession(name string, intended, resolved api.Backend, src api.BackendCredentialSource, home, label string) api.Session {
	s := api.Session{Name: name, Workspace: "ws", Team: "squad", Role: "worker", State: api.SessionRunning, PaneID: "%3"}
	s.Runtime.Name = "claude"
	s.BackendIntended = intended
	s.BackendResolved = resolved
	s.BackendCredentialSource = src
	s.AccountHome = home
	s.Runtime.AccountLabel = label
	return s
}

// svcCells renders name,service for the sessions and returns the SERVICE cell
// by agent name. The cell is the text after the name field.
func svcCells(t *testing.T, sessions ...api.Session) map[string]string {
	t.Helper()
	table := renderSessionTableCols(sessions, columnsOrFatal(t, "name,service", nil))
	out := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(table, "\n"), "\n")[1:] {
		name, cell, _ := strings.Cut(strings.TrimSpace(line), " ")
		out[name] = strings.TrimSpace(cell)
	}
	return out
}

// SERVICE is registered, selectable by name, opt-in, and not in the wide set:
// it shows only when asked for (svccol-1).
func TestServiceColumnIsOptInNotWide(t *testing.T) {
	c, ok := lookupSessionColumn("service")
	if !ok {
		t.Fatal("service is not a registered column")
	}
	if c.header != "SERVICE" {
		t.Errorf("header = %q, want SERVICE", c.header)
	}
	if !optInColumns["service"] {
		t.Error("service is not opt-in, so the default table would gain a column")
	}
	if slices.Contains(wideSessionColumns, "service") {
		t.Error("service is in the wide set")
	}
	if slices.Contains(fitPriority, "service") {
		t.Error("service is in fitPriority, so it would not be the first rest column cut")
	}
	if _, ok := lookupSessionColumn("acct"); !ok {
		t.Error("acct is gone; SERVICE sits beside ACCT and does not replace it")
	}
}

// The backend field prints the raw resolved backend, with the intent only
// where it differs (S3).
func TestServiceBackendCell(t *testing.T) {
	for _, tc := range []struct {
		name               string
		intended, resolved api.Backend
		want               string
	}{
		{"resolved empty prints a dash", "", "", "claude - ambient default-home"},
		{"intent empty prints resolved", "", api.BackendBedrock, "claude bedrock ambient default-home"},
		{"subscription on default prints subscription", api.BackendSubscription, api.BackendDefaultName, "claude subscription ambient default-home"},
		{"subscription resolved to bedrock is flagged", api.BackendSubscription, api.BackendBedrock, "claude bedrock!subscription ambient default-home"},
		{"bedrock on bedrock prints bedrock", api.BackendBedrock, api.BackendBedrock, "claude bedrock ambient default-home"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := svcCells(t, svcSession("a", tc.intended, tc.resolved, api.BackendCredentialAmbient, "", ""))["a"]
			if got != tc.want {
				t.Errorf("cell = %q, want %q", got, tc.want)
			}
		})
	}
}

// A session marvel never classified prints dashes, not the default backend
// that AccountKeyOf would assume.
func TestServiceNeverClassifiedPrintsDashes(t *testing.T) {
	got := svcCells(t, svcSession("a", "", "", "", "", ""))["a"]
	if got != "claude - - default-home" {
		t.Errorf("cell = %q, want %q", got, "claude - - default-home")
	}
}

// The home field is the last element of the cleaned home.
func TestServiceHomeIsLastElement(t *testing.T) {
	got := svcCells(t, svcSession("a", "", api.BackendDefaultName, api.BackendCredentialAmbient, "/Users/x/.claude-acct/alt-ultra/", ""))["a"]
	if got != "claude default ambient alt-ultra" {
		t.Errorf("cell = %q, want the home shown as its last element alt-ultra", got)
	}
}

// Two distinct homes that share a last element print in full; rows on one home
// do not collide with themselves (X4).
func TestServiceHomeCollisionCountsDistinctHomes(t *testing.T) {
	mk := func(name, home string) api.Session {
		return svcSession(name, "", api.BackendDefaultName, api.BackendCredentialAmbient, home, "")
	}
	got := svcCells(t, mk("a", "/a/x"), mk("b", "/b/x"), mk("c", "/b/y"))
	if want := "claude default ambient /a/x"; got["a"] != want {
		t.Errorf("a = %q, want %q", got["a"], want)
	}
	if want := "claude default ambient /b/x"; got["b"] != want {
		t.Errorf("b = %q, want %q", got["b"], want)
	}
	if want := "claude default ambient y"; got["c"] != want {
		t.Errorf("c = %q, want %q (its last element is not shared)", got["c"], want)
	}
	same := svcCells(t, mk("a", "/a/x"), mk("b", "/a/x"))
	for _, n := range []string{"a", "b"} {
		if want := "claude default ambient x"; same[n] != want {
			t.Errorf("%s = %q, want %q (one home is not a collision)", n, same[n], want)
		}
	}
}

// The collision count is per cluster, over every row of the reply (X4).
func TestServiceCollisionIsPerCluster(t *testing.T) {
	mk := func(name, cluster, home string) api.Session {
		s := svcSession(name, "", api.BackendDefaultName, api.BackendCredentialAmbient, home, "")
		s.Cluster = cluster
		return s
	}
	got := svcCells(t, mk("a", "k", "/a/x"), mk("b", "m", "/b/x"))
	for _, n := range []string{"a", "b"} {
		if want := "claude default ambient x"; got[n] != want {
			t.Errorf("%s = %q, want %q (the same last element on two clusters is not a collision)", n, got[n], want)
		}
	}
	both := svcCells(t, mk("a", "k", "/a/x"), mk("b", "k", "/b/x"))
	if want := "claude default ambient /a/x"; both["a"] != want {
		t.Errorf("a = %q, want %q (a collision inside one cluster)", both["a"], want)
	}
}

// A home whose last element is literally default-home always prints in full,
// so it cannot read as the empty home.
func TestServiceLiteralDefaultHomeAlwaysFullPath(t *testing.T) {
	got := svcCells(t, svcSession("a", "", api.BackendDefaultName, api.BackendCredentialAmbient, "/p/default-home", ""))["a"]
	if want := "claude default ambient /p/default-home"; got != want {
		t.Errorf("cell = %q, want %q", got, want)
	}
}

// The label prints as declared after the home; absent, nothing follows, with
// no trailing space (svccol-2).
func TestServiceLabelPrintsAsWrittenAbsentPrintsNothing(t *testing.T) {
	with := svcSession("a", api.BackendSubscription, api.BackendDefaultName, api.BackendCredentialAmbient, "/h/alt-ultra", "ultra")
	without := svcSession("b", api.BackendSubscription, api.BackendDefaultName, api.BackendCredentialAmbient, "/h/alt-ultra", "")
	homes := countServiceHomes([]api.Session{with, without})
	if got, want := newSessionRow(with, homes).service, "claude subscription ambient alt-ultra ultra"; got != want {
		t.Errorf("labelled cell = %q, want %q", got, want)
	}
	if got, want := newSessionRow(without, homes).service, "claude subscription ambient alt-ultra"; got != want {
		t.Errorf("unlabelled cell = %q, want %q with no trailing space", got, want)
	}
}

// A label is declared text and goes through the same draw filter as every cell:
// an escape in it is replaced, and the tally counts the field.
func TestServiceLabelWithEscIsDrawnAsPlaceholder(t *testing.T) {
	s := svcSession("a", "", api.BackendDefaultName, api.BackendCredentialAmbient, "/h/x", "ul\x1b[31mtra")
	cols := drawableColumns(columnsOrFatal(t, "name,service", nil))
	table := renderSessionTableCols([]api.Session{s}, cols)
	if strings.ContainsRune(table, 0x1b) {
		t.Errorf("the table carries an escape byte: %q", table)
	}
	var tally undrawTally
	tally.cells([]api.Session{s}, columnsOrFatal(t, "name,service", nil))
	if tally.fields != 1 {
		t.Errorf("tally counted %d fields, want 1 (the label)", tally.fields)
	}
}

// The table, the legend scan and the undrawable tally all build rows from the
// same home count (S8), so a collision reads the same in each.
func TestServiceCellSameAcrossHoldLastActiveAndTable(t *testing.T) {
	a := svcSession("a", "", api.BackendDefaultName, api.BackendCredentialAmbient, "/a/x", "")
	b := svcSession("b", "", api.BackendDefaultName, api.BackendCredentialAmbient, "/b/x", "")
	sessions := []api.Session{a, b}
	table := svcCells(t, sessions...)
	rows := map[string]string{
		"a": newSessionRow(a, countServiceHomes(sessions)).service,
		"b": newSessionRow(b, countServiceHomes(sessions)).service,
	}
	for n, want := range rows {
		if table[n] != want {
			t.Errorf("%s: table cell %q differs from the row cell %q", n, table[n], want)
		}
	}
	if !strings.Contains(table["a"], "/a/x") {
		t.Errorf("a = %q, want the full path (a collision with b)", table["a"])
	}
	// The tally's cells and the legend scan take the whole reply, not a row.
	var tally undrawTally
	tally.cells(sessions, columnsOrFatal(t, "name,service", nil))
	if tally.fields != 0 {
		t.Errorf("tally counted %d fields on clean cells", tally.fields)
	}
	if sourceMarkOnScreen(sessions, columnsOrFatal(t, "name,service", nil)) {
		t.Error("the legend scan found a source mark in SERVICE")
	}
}

// The source field names the bearer kinds the tmux environment held at spawn
// (svccol-3). A record from before the field reads as plain ambient (V2).
func TestServiceAmbientCells(t *testing.T) {
	for _, tc := range []struct {
		bearer string
		want   string
	}{
		{"none", "claude default ambient default-home"},
		{"api-key+oauth-token", "claude default ambient:api-key+oauth-token default-home"},
		{"auth-token", "claude default ambient:auth-token default-home"},
		{"unknown", "claude default ambient:? default-home"},
		{"", "claude default ambient default-home"},
	} {
		s := svcSession("a", "", api.BackendDefaultName, api.BackendCredentialAmbient, "", "")
		s.AmbientBearer = tc.bearer
		if got := svcCells(t, s)["a"]; got != tc.want {
			t.Errorf("bearer %q: cell = %q, want %q", tc.bearer, got, tc.want)
		}
	}
	other := svcSession("b", "", api.BackendDefaultName, api.BackendCredentialAWSProfile, "", "")
	other.AmbientBearer = "api-key"
	if got, want := svcCells(t, other)["b"], "claude default aws-profile default-home"; got != want {
		t.Errorf("a non-ambient source: cell = %q, want %q", got, want)
	}
}

// A stored value outside the closed set (a reply from another daemon version)
// prints as unknown, and its text never reaches the screen.
func TestServiceOutOfSetBearerPrintsUnknown(t *testing.T) {
	s := svcSession("a", "", api.BackendDefaultName, api.BackendCredentialAmbient, "", "")
	s.AmbientBearer = "api-key+SENTINEL"
	got := svcCells(t, s)["a"]
	if got != "claude default ambient:? default-home" || strings.Contains(got, "SENTINEL") {
		t.Errorf("cell = %q, want ambient:? and no stored text", got)
	}
}
