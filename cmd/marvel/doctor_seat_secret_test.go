package main

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// A seat that declared a secret and started without it is a warning that names
// the seat, the need and why, and never a value. A seat with no note adds none.
func TestDoctorWarnsOfASeatStartedWithoutItsSecret(t *testing.T) {
	in := fakeHost(t)
	in.Sessions = func() ([]api.Session, error) {
		return []api.Session{
			{Name: "builder-0", Pane: "idle", SecretNote: "seat secret bd: BEADS_DOLT_PASSWORD is not set in the daemon, so the seat starts without it"},
			{Name: "builder-1", Pane: "idle"},
		}, nil
	}
	out, failed := doctorRun(in)
	if failed != 0 {
		t.Fatalf("failed = %d, want 0\n%s", failed, out)
	}
	l := lineWith(out, "seat-secret")
	if !strings.HasPrefix(l, "WARN") || !strings.Contains(l, "builder-0") || !strings.Contains(l, "BEADS_DOLT_PASSWORD") {
		t.Fatalf("seat-secret line = %q, want a WARN naming the seat and the variable\n%s", l, out)
	}
	if strings.Contains(l, "builder-1") {
		t.Errorf("the seat with no note is named: %q", l)
	}
}

func TestDoctorSaysNothingOfSeatSecretsWhenNoSeatMissedOne(t *testing.T) {
	in := fakeHost(t)
	in.Sessions = func() ([]api.Session, error) {
		return []api.Session{{Name: "builder-1", Pane: "idle"}}, nil
	}
	out, _ := doctorRun(in)
	if l := lineWith(out, "seat-secret"); l != "" {
		t.Errorf("a seat-secret line with no note: %q", l)
	}
}
