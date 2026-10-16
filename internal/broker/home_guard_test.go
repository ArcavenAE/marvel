package broker

import (
	"os"
	"testing"

	"github.com/arcavenae/marvel/internal/homeguard"
)

// TestMain gives every test in the package a scratch HOME and no MARVEL_*
// variables through the shared guard; see internal/homeguard.
func TestMain(m *testing.M) { os.Exit(homeguard.Run(m)) }
