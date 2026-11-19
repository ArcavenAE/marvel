package fake_test

import (
	"testing"

	"github.com/arcavenae/marvel/internal/runtime"
	"github.com/arcavenae/marvel/internal/tmux/tmuxtest/fake"
)

var _ runtime.PaneController = (*fake.Driver)(nil)

func TestFakeKeepsSessionsAndPanesInMemory(t *testing.T) {
	d := fake.New()
	if err := d.NewSession("s"); err != nil {
		t.Fatal(err)
	}
	id, err := d.NewPaneAt("s", "sleep 1", "t", "", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := d.PaneStatus(id); !st.Exists || st.Dead {
		t.Fatalf("new pane status = %+v, want live", st)
	}
	d.Exit(id, "3")
	if st, _ := d.PaneStatus(id); !st.Dead || st.ExitStatus != "3" {
		t.Fatalf("exited pane status = %+v", st)
	}
	if err := d.KillSession("s"); err != nil {
		t.Fatal(err)
	}
	if st, _ := d.PaneStatus(id); st.Exists {
		t.Fatal("pane survived its session")
	}
	if names, _ := d.ListSessions(); len(names) != 0 {
		t.Fatalf("sessions = %v", names)
	}
}
