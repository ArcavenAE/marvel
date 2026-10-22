package main

import (
	"encoding/json"
	"io"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// tiedRows holds rows that tie on name across workspaces and on workspace and
// name across teams, so a sort on name alone leaves their order to the input.
func tiedRows() []api.Session {
	return []api.Session{
		{Workspace: "a", Name: "seat", Team: "t1", Role: "r1", Generation: 1, PaneID: "%1"},
		{Workspace: "a", Name: "seat", Team: "t1", Role: "r1", Generation: 2, PaneID: "%2"},
		{Workspace: "a", Name: "seat", Team: "t2", Role: "r1", Generation: 1, PaneID: "%3"},
		{Workspace: "b", Name: "seat", Team: "t1", Role: "r1", Generation: 1, PaneID: "%4"},
		{Workspace: "b", Name: "zed", Team: "t1", Role: "r1", Generation: 1, PaneID: "%5"},
		{Workspace: "c", Name: "alpha", Team: "t1", Role: "r2", Generation: 1, PaneID: "%6"},
	}
}

func rowOrder(rows []api.Session) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(r.PaneID + " ")
	}
	return b.String()
}

func shuffled(rng *rand.Rand, rows []api.Session) []api.Session {
	out := append([]api.Session(nil), rows...)
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// printedPanes runs printSessions with the desk column and returns the desk
// cells in the order they were drawn.
func printedPanes(t *testing.T, rows []api.Session) []string {
	t.Helper()
	data, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	perr := printSessions(data, []sessionColumn{mustColumn(t, "desk")}, "", fitOptions{})
	os.Stdout = old
	_ = w.Close()
	out, _ := io.ReadAll(r)
	if perr != nil {
		t.Fatalf("printSessions: %v", perr)
	}
	lines := strings.Fields(string(out))
	return lines[1:]
}

// Whatever order the daemon answers in, get sessions draws workspace, then
// name, then the full tiebreak, so a refresh never swaps tied rows.
func TestSortStableOnWorkspaceName(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	var want []string
	for i := 0; i < 100; i++ {
		got := printedPanes(t, shuffled(rng, tiedRows()))
		if i == 0 {
			want = got
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("shuffle %d drew %v, the first drew %v", i, got, want)
		}
	}
	// workspace first, then name, then team, role and generation.
	if got, w := strings.Join(want, " "), "6 1 2 3 4 5"; got != w {
		t.Errorf("order = %s, want %s", got, w)
	}
}

// The watch sort breaks a tie on the chosen column the same way, in either
// direction, so a watch frame does not shuffle tied rows between refreshes.
func TestWatchSortTieIsDeterministic(t *testing.T) {
	for _, desc := range []bool{false, true} {
		rng := rand.New(rand.NewSource(13))
		var want string
		for i := 0; i < 100; i++ {
			rows := shuffled(rng, tiedRows())
			sortSessions(rows, &watchSort{column: "state", desc: desc})
			got := rowOrder(rows)
			if i == 0 {
				want = got
				continue
			}
			if got != want {
				t.Fatalf("desc=%v shuffle %d gave %s, the first gave %s", desc, i, got, want)
			}
		}
	}
}
