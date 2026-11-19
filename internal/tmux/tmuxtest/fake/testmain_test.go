package fake_test

import (
	"os"
	"testing"

	"github.com/arcavenae/marvel/internal/homeguard"
)

func TestMain(m *testing.M) { os.Exit(homeguard.Run(m)) }
