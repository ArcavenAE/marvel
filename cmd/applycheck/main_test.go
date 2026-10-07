package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

const fixture = "testdata/manifest.toml"

func output(t *testing.T, args ...string) map[string]map[string]json.RawMessage {
	t.Helper()
	var buf bytes.Buffer
	if err := run(args, &buf); err != nil {
		t.Fatalf("run %v: %v", args, err)
	}
	var got map[string]map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not one JSON object keyed by team: %v\n%s", err, buf.String())
	}
	return got
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// The contract: one JSON object keyed by team name with no workspace prefix,
// each value holding exactly WorkDir, Roles and Budget.
func TestApplycheckPrintsOneObjectKeyedByTeam(t *testing.T) {
	got := output(t, fixture, "/srv/root")
	if want := []string{"placed", "plain"}; !reflect.DeepEqual(keys(got), want) {
		t.Fatalf("team keys = %v, want %v", keys(got), want)
	}
	for team, v := range got {
		if want := []string{"Budget", "Roles", "WorkDir"}; !reflect.DeepEqual(keys(v), want) {
			t.Errorf("team %s fields = %v, want %v", team, keys(v), want)
		}
	}
}

// The second argument is the workspace root: a team that declares no workdir
// is anchored at it, and one that declares its own keeps it.
func TestApplycheckAnchorsAtTheRootArgument(t *testing.T) {
	got := output(t, fixture, "/srv/root")
	var plain, placed string
	_ = json.Unmarshal(got["plain"]["WorkDir"], &plain)
	_ = json.Unmarshal(got["placed"]["WorkDir"], &placed)
	if plain != "/srv/root" {
		t.Errorf("plain WorkDir = %q, want the root argument /srv/root", plain)
	}
	if placed != "/srv/placed" {
		t.Errorf("placed WorkDir = %q, want its declared /srv/placed", placed)
	}
}

// Roles carry the declared role set, so a drift in the roles an apply holds
// shows in the comparison the upgrade makes.
func TestApplycheckRolesCarryTheDeclaredRoles(t *testing.T) {
	got := output(t, fixture, "/srv/root")
	var roles []struct {
		Name     string
		Replicas int
	}
	if err := json.Unmarshal(got["plain"]["Roles"], &roles); err != nil {
		t.Fatal(err)
	}
	if len(roles) != 1 || roles[0].Name != "worker" || roles[0].Replicas != 2 {
		t.Errorf("plain roles = %+v, want one worker with 2 replicas", roles)
	}
}

// A manifest that does not read, parse or apply is an error, which main turns
// into a nonzero exit; the upgrade reads that as "does not parse or apply".
func TestApplycheckErrorsOnABadManifest(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.toml")
	if err := os.WriteFile(bad, []byte("[workspace\nname = "), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{bad, "/srv/root"}, {"/no/such/manifest.toml", "/srv/root"}, {fixture}, {}} {
		var buf bytes.Buffer
		if err := run(args, &buf); err == nil {
			t.Errorf("run %v: no error, want one", args)
		}
		if buf.Len() != 0 {
			t.Errorf("run %v printed %q before failing; a failed check prints nothing", args, buf.String())
		}
	}
}

// Budget is printed as the store holds it: the declared ceiling for a team
// that has one, and the zero value for a team that does not.
func TestApplycheckPrintsTheDeclaredBudget(t *testing.T) {
	got := output(t, fixture, "/srv/root")
	var placed, plain map[string]any
	_ = json.Unmarshal(got["placed"]["Budget"], &placed)
	_ = json.Unmarshal(got["plain"]["Budget"], &plain)
	if placed["max_sessions"] != float64(7) {
		t.Errorf("placed Budget = %v, want max_sessions 7", placed)
	}
	if len(plain) != 0 {
		t.Errorf("plain Budget = %v, want no declared clause", plain)
	}
}
