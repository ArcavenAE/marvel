package upgrade

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arcavenae/marvel/internal/paths"
)

// KeepRunning is a seam in this link: it keeps nothing and returns the zero
// Fallback, so a caller wired to it today changes nothing on disk.
func TestKeepRunningIsANoOp(t *testing.T) {
	home := t.TempDir()
	l := paths.Layout{Home: home}
	got, err := KeepRunning(l, filepath.Join(home, "marvel"))
	if err != nil || got != (Fallback{}) {
		t.Errorf("KeepRunning = %+v, %v; want the zero Fallback and nil", got, err)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Errorf("KeepRunning wrote under the home: %v, %v", entries, err)
	}
}
