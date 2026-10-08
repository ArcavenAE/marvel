package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// ageNow is the clock the AGE tests read, the one LAST-ACTIVE's tests pin.
var ageNow = lastActiveNow

// ageSession is a session created ago before ageNow; a negative ago leaves
// CreatedAt zero, as a held role's synthetic row has it.
func ageSession(name string, ago time.Duration) api.Session {
	s := api.Session{Name: name, Workspace: "ws", Team: "squad", Role: "worker", State: api.SessionRunning, PaneID: "%3"}
	if ago >= 0 {
		s.CreatedAt = ageNow.Add(-ago)
	}
	return s
}

func ageCells(t *testing.T, sessions ...api.Session) map[string]string {
	t.Helper()
	withLastActiveClock(t)
	table := renderSessionTableCols(sessions, columnsOrFatal(t, "name,age", nil))
	out := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(table, "\n"), "\n")[1:] {
		cells := splitColumns(line)
		out[cells[0]] = cells[1]
	}
	return out
}

// AGE is registered, opt-in (a pipe prints what it always printed), and part
// of the wide set.
func TestAgeColumnIsRegisteredOptInAndWide(t *testing.T) {
	c, ok := lookupSessionColumn("age")
	if !ok {
		t.Fatal("age is not a registered column")
	}
	if c.header != "AGE" {
		t.Errorf("header = %q, want AGE", c.header)
	}
	if !optInColumns["age"] {
		t.Error("age is not opt-in, so the default table would gain a column")
	}
	if !slices.Contains(wideSessionColumns, "age") {
		t.Error("age is not in the wide set")
	}
	for _, d := range defaultSessionColumns() {
		if d.name == "age" {
			t.Error("age is in the default columns")
		}
	}
}

// AGE is the time since the session was created, in the age words LAST-ACTIVE
// uses, with days past one day so a long-lived seat does not read 120h0m.
func TestAgeCellReadsCreatedAt(t *testing.T) {
	for _, tc := range []struct {
		name string
		ago  time.Duration
		want string
	}{
		{"a-zero", 0, "0s"},
		{"b-seconds", 45 * time.Second, "45s"},
		{"c-minutes", 3 * time.Minute, "3m"},
		{"d-hours", 2*time.Hour + 9*time.Minute, "2h9m"},
		{"e-just-under-a-day", 23*time.Hour + 59*time.Minute, "23h59m"},
		{"f-a-day", 24 * time.Hour, "1d0h"},
		{"g-days", 3*24*time.Hour + 4*time.Hour + 30*time.Minute, "3d4h"},
		{"h-unset", -1, "-"},
		{"i-future", -2, "0s"},
	} {
		s := ageSession(tc.name, tc.ago)
		if tc.ago == -2 {
			s.CreatedAt = ageNow.Add(time.Minute)
		}
		got := ageCells(t, s)[tc.name]
		if got != tc.want {
			t.Errorf("%s: cell = %q, want %q", tc.name, got, tc.want)
		}
	}
}
