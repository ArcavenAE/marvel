package team

import (
	"fmt"
	"os"
	"testing"

	"github.com/arcavenae/marvel/internal/homeguard"
	"github.com/arcavenae/marvel/internal/tmux/tmuxtest"
)

// One tmux server for this test binary; see tmuxtest.StartServer.
func TestMain(m *testing.M) {
	srv, err := tmuxtest.StartServer("team")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := homeguard.RunWith(m, srv.Setup)
	srv.Stop()
	os.Exit(code)
}
