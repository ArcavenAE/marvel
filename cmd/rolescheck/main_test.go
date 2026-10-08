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

// The contract: one JSON object keyed by team name, each value holding Roles
// and nothing else, so it builds and compares on commits whose team record has
// no WorkDir.
func TestRolescheckPrintsRolesOnlyKeyedByTeam(t *testing.T) {
	got := output(t, fixture)
	if want := []string{"placed", "plain"}; !reflect.DeepEqual(keys(got), want) {
		t.Fatalf("team keys = %v, want %v", keys(got), want)
	}
	for team, v := range got {
		if want := []string{"Roles"}; !reflect.DeepEqual(keys(v), want) {
			t.Errorf("team %s fields = %v, want only %v", team, keys(v), want)
		}
	}
}

func TestRolescheckRolesCarryTheDeclaredRoles(t *testing.T) {
	got := output(t, fixture)
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

// With no manifest argument, or one that does not read, parse or apply, the
// command errors and writes nothing to its output.
func TestRolescheckErrorsOnABadManifest(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.toml")
	if err := os.WriteFile(bad, []byte("[workspace\nname = "), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{bad}, {"/no/such/manifest.toml"}, {}} {
		var buf bytes.Buffer
		if err := run(args, &buf); err == nil {
			t.Errorf("run %v: no error, want one", args)
		}
		if buf.Len() != 0 {
			t.Errorf("run %v printed %q before failing; a failed check writes nothing to its output", args, buf.String())
		}
	}
}
