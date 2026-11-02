package api

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// The first-cluster example is the file a first user applies unedited, so both
// formats must say the same thing: one team of one supervisor and one worker
// on the simulator, each with a plain prompt, and no path of any kind that is
// tied to a home directory or to marvel's own manifest directory.
func TestExampleFirstClusterManifestsAgree(t *testing.T) {
	t.Parallel()
	paths := []string{"../../examples/first-cluster.toml", "../../examples/first-cluster.yaml"}
	type seat struct {
		Name     string
		Replicas int
		Image    string
		Command  string
		Args     []string
		Prompt   string
	}
	var first []seat
	for i, p := range paths {
		m, err := ParseManifest(p)
		if err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		if m.Workspace.Name != "first-cluster" || len(m.Teams) != 1 || m.Teams[0].Name != "crew" {
			t.Fatalf("%s: want workspace first-cluster with the one team crew, got %q and %d teams", p, m.Workspace.Name, len(m.Teams))
		}
		var seats []seat
		for _, r := range m.Teams[0].Roles {
			seats = append(seats, seat{r.Name, r.Replicas, r.Runtime.Image, r.Runtime.Command, r.Runtime.Args, r.Runtime.Prompt})
		}
		if len(seats) != 2 || seats[0].Name != "supervisor" || seats[1].Name != "worker" {
			t.Fatalf("%s: want the roles supervisor and worker, got %+v", p, seats)
		}
		for _, s := range seats {
			if s.Replicas != 1 || s.Prompt == "" || s.Command != "marvel-simulator" || s.Image != "simulator" {
				t.Errorf("%s: role %s is not one simulator seat with a prompt: %+v", p, s.Name, s)
			}
		}
		if i == 0 {
			first = seats
		} else if !reflect.DeepEqual(first, seats) {
			t.Errorf("the two formats disagree:\n%s: %+v\n%s: %+v", paths[0], first, p, seats)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"/Users/", "/home/", "~/.marvel/manifests"} {
			if strings.Contains(string(raw), bad) {
				t.Errorf("%s names %q; a first-run example holds no such path", p, bad)
			}
		}
	}
}
