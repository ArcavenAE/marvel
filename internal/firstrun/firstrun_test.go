package firstrun

import (
	"os"
	"testing"

	"github.com/arcavenae/marvel/internal/homeguard"
)

func TestMain(m *testing.M) { os.Exit(homeguard.Run(m)) }

// The framework carries only the usage-limit row. Each further screen joins the
// table in its own change, once its capture exists.
func TestTableCarriesOnlyTheUsageLimitRow(t *testing.T) {
	rows := Rows()
	if len(rows) != 1 {
		t.Fatalf("%d rows, want 1", len(rows))
	}
	r := rows[0]
	if r.ID != "usage-limit" || r.Action != ActionKey {
		t.Errorf("row = %+v, want id usage-limit with the key action", r)
	}
}

// Every row names its screen, the pattern that finds it and what marvel does,
// and the ids are unique, so a row added later cannot be half written.
func TestEveryRowIsComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range Rows() {
		if r.ID == "" || r.Screen == "" || r.Pattern == "" || r.Answer == "" {
			t.Errorf("row %+v has an empty field", r)
		}
		if r.Action != ActionKey && r.Action != ActionPark {
			t.Errorf("row %s has action %q, want key or park", r.ID, r.Action)
		}
		if seen[r.ID] {
			t.Errorf("row id %s appears twice", r.ID)
		}
		seen[r.ID] = true
	}
	if len(seen) == 0 {
		t.Error("the table is empty")
	}
}
