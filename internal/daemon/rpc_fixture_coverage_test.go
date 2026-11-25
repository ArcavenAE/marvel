package daemon

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestEveryServedMethodHasAWireFixture holds the method table to the golden
// fixtures of rpc_golden_test.go: each served method has at least one fixture,
// "<method>.params.json" or "<method>.result.json", under
// internal/daemonrpc/testdata/rpc. Without it a method can ship with no wire
// pin, and the golden test, which walks the fixtures it already has, would
// never notice (I-458 row 24, aae-orc-va2sz).
func TestEveryServedMethodHasAWireFixture(t *testing.T) {
	t.Parallel()
	var missing []string
	for name := range methodTable {
		found := false
		for _, kind := range []string{".params.json", ".result.json"} {
			if _, err := os.Stat(filepath.Join(rpcGoldenDir, name+kind)); err == nil {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%d of %d served methods have no fixture under %s: %s",
			len(missing), len(methodTable), rpcGoldenDir, strings.Join(missing, ", "))
	}
}
