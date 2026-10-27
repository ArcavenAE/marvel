package main

import (
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// TestSortByDeskIsNumeric: desk ids are tmux pane numbers, so %100 sorts
// after %29, not before it. A desk with no number (empty, or not a number)
// sorts first ascending and last descending, as rateSortKey treats a missing
// rate.
func TestSortByDeskIsNumeric(t *testing.T) {
	t.Parallel()
	rows := func() []api.Session {
		return []api.Session{
			{Workspace: "w", Name: "a", PaneID: "%100"},
			{Workspace: "w", Name: "b", PaneID: "%9"},
			{Workspace: "w", Name: "c", PaneID: ""},
			{Workspace: "w", Name: "d", PaneID: "%29"},
			{Workspace: "w", Name: "e", PaneID: "%x"},
		}
	}
	cases := []struct {
		desc bool
		want string
	}{
		{false, " %x %9 %29 %100 "},
		{true, "%100 %29 %9 %x  "},
	}
	for _, c := range cases {
		s := rows()
		sortSessions(s, &watchSort{column: "desk", desc: c.desc})
		if got := rowOrder(s); got != c.want {
			t.Errorf("desc=%v: order %q, want %q", c.desc, got, c.want)
		}
	}
}
