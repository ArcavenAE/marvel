package timing

import (
	"testing"

	"github.com/arcavenae/marvel/internal/events"
)

// The store-write site (aae-orc-6layq, I-797 S5) has the stable name its report
// and overrun events use, and no provisional budget: slice 1 sets budgets from
// a week of data, so a later change to provisionalBudget that gives it one
// shows up here.
func TestStoreWriteSiteHasNoProvisionalBudget(t *testing.T) {
	t.Parallel()
	if SiteStoreWrite != "store.write" {
		t.Errorf("SiteStoreWrite = %q, want %q", SiteStoreWrite, "store.write")
	}
	if got := provisionalBudget(SiteStoreWrite); got != 0 {
		t.Errorf("provisionalBudget(%s) = %v, want 0 (histogram only)", SiteStoreWrite, got)
	}
	reg := NewRegistry(events.NewRing(16), newClock().now)
	reg.UseProvisionalBudgets()
	reg.Record(SiteStoreWrite, 5)
	for _, s := range reg.Report().Sites {
		if s.Name == SiteStoreWrite && s.Budget != 0 {
			t.Errorf("the %s site has budget %v under provisional budgets, want 0", SiteStoreWrite, s.Budget)
		}
	}
}
