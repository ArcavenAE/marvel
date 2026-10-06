package api

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// viewManifest is one team with one role carrying the given [[team.role.view]]
// tables, written as TOML. Replicas is 0 so nothing would ever spawn.
func viewManifest(views ...string) string {
	return `
[workspace]
name = "test"

[[team]]
name = "squad"

  [[team.role]]
  name = "reader"
  replicas = 0

    [team.role.runtime]
    command = "claude"
` + strings.Join(views, "\n")
}

// viewTable renders one [[team.role.view]] table from its lines.
func viewTable(lines ...string) string {
	return "\n    [[team.role.view]]\n    " + strings.Join(lines, "\n    ") + "\n"
}

func appliedViews(t *testing.T, manifest string) []View {
	t.Helper()
	m, err := ParseManifestBytes([]byte(manifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	team, err := store.GetTeam("test/squad")
	if err != nil {
		t.Fatal(err)
	}
	return team.Roles[0].Views
}

// Apply refuses a duplicate name in one role, a name that is not one path
// element, and a refresh_every below the floor (readonly-view.md section 2).
func TestViewRefusals(t *testing.T) {
	t.Parallel()
	ok := func(name string) string {
		return viewTable(fmt.Sprintf("name = %q", name), `remote = "git@example.com:o/r.git"`, `ref = "main"`)
	}
	cases := []struct {
		name     string
		manifest string
		want     string
	}{
		{"duplicate name", viewManifest(ok("repo"), ok("repo")), `view name "repo" is duplicated`},
		{"slash in name", viewManifest(ok("a/b")), "must be one path element"},
		{"dot name", viewManifest(ok(".")), "must be one path element"},
		{"dot dot name", viewManifest(ok("..")), "must be one path element"},
		{"empty name", viewManifest(ok("")), "name is required"},
		{"below the floor", viewManifest(viewTable(`name = "repo"`, `remote = "r"`, `ref = "main"`, `refresh_every = "30s"`)), "below the 1m0s floor"},
		{"not a duration", viewManifest(viewTable(`name = "repo"`, `remote = "r"`, `ref = "main"`, `refresh_every = "soon"`)), "refresh_every"},
		{"one second under the floor", viewManifest(viewTable(`name = "repo"`, `remote = "r"`, `ref = "main"`, `refresh_every = "59s"`)), "below the 1m0s floor"},
		{"reenter_grace not a duration", viewManifest(viewTable(`name = "repo"`, `remote = "r"`, `ref = "main"`, `reenter_grace = "soon"`)), "reenter_grace"},
		{"negative reenter_grace", viewManifest(viewTable(`name = "repo"`, `remote = "r"`, `ref = "main"`, `reenter_grace = "-1m"`)), "reenter_grace"},
		{"no remote", viewManifest(viewTable(`name = "repo"`, `ref = "main"`)), "remote is required"},
		{"no ref", viewManifest(viewTable(`name = "repo"`, `remote = "r"`)), "ref is required"},
		{"backslash in name", viewManifest(ok("a\\b")), "must be one path element"},
		{"dot in name", viewManifest(ok("a.b")), "must be one path element"},
		{"space in name", viewManifest(ok("a b")), "must be one path element"},
		{"equals in name", viewManifest(ok("a=b")), "must be one path element"},
		{"newline in name", viewManifest(ok("a\nb")), "must be one path element"},
		{"names that share a variable", viewManifest(ok("a-b"), ok("a_b")), "MARVEL_VIEW_A_B"},
		{"names that differ only in case", viewManifest(ok("Repo"), ok("repo")), "MARVEL_VIEW_REPO"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseManifestBytes([]byte(tc.manifest))
			if err == nil {
				t.Fatalf("parsed, want a refusal containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// Two roles may each declare a view of the same name: the refusal is per role.
func TestViewSameNameInTwoRolesIsAllowed(t *testing.T) {
	t.Parallel()
	manifest := `
[workspace]
name = "test"

[[team]]
name = "squad"

  [[team.role]]
  name = "a"
  replicas = 0
    [team.role.runtime]
    command = "claude"
    [[team.role.view]]
    name = "repo"
    remote = "r"
    ref = "main"

  [[team.role]]
  name = "b"
  replicas = 0
    [team.role.runtime]
    command = "claude"
    [[team.role.view]]
    name = "repo"
    remote = "r"
    ref = "main"
`
	if _, err := ParseManifestBytes([]byte(manifest)); err != nil {
		t.Fatalf("parse: %v", err)
	}
}

func TestViewDefaults(t *testing.T) {
	t.Parallel()
	views := appliedViews(t, viewManifest(
		viewTable(`name = "plain"`, `remote = "r"`, `ref = "main"`),
		viewTable(`name = "tuned"`, `remote = "r2"`, `ref = "trunk"`, `refresh_every = "1m"`, `reenter_grace = "30s"`),
	))
	want := []View{
		{Name: "plain", Remote: "r", Ref: "main", RefreshEvery: 10 * time.Minute, ReenterGrace: 2 * time.Minute},
		{Name: "tuned", Remote: "r2", Ref: "trunk", RefreshEvery: time.Minute, ReenterGrace: 30 * time.Second},
	}
	if !reflect.DeepEqual(views, want) {
		t.Errorf("views = %+v, want %+v", views, want)
	}
}

// The same declaration in TOML and in YAML yields the same views.
func TestViewTOMLAndYAMLParseTheSame(t *testing.T) {
	t.Parallel()
	tomlViews := appliedViews(t, viewManifest(
		viewTable(`name = "my-repo"`, `remote = "r"`, `ref = "main"`, `refresh_every = "5m"`, `reenter_grace = "1m"`),
	))
	yamlManifest := `
workspace:
  name: test
teams:
  - name: squad
    roles:
      - name: reader
        replicas: 0
        runtime:
          command: claude
        views:
          - name: my-repo
            remote: r
            ref: main
            refresh_every: 5m
            reenter_grace: 1m
`
	yamlViews := appliedViews(t, yamlManifest)
	if len(tomlViews) != 1 || !reflect.DeepEqual(tomlViews, yamlViews) {
		t.Errorf("toml views %+v, yaml views %+v, want one identical view", tomlViews, yamlViews)
	}
}

// A role snapshot taken from the store is not shared with the store.
func TestViewsAreClonedOutOfTheStore(t *testing.T) {
	t.Parallel()
	manifest := viewManifest(viewTable(`name = "repo"`, `remote = "r"`, `ref = "main"`))
	m, err := ParseManifestBytes([]byte(manifest))
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatal(err)
	}
	first, _ := store.GetTeam("test/squad")
	if len(first.Roles[0].Views) != 1 {
		t.Fatalf("views = %+v, want one", first.Roles[0].Views)
	}
	first.Roles[0].Views[0].Name = "changed"
	again, _ := store.GetTeam("test/squad")
	if again.Roles[0].Views[0].Name != "repo" {
		t.Errorf("a caller edit reached the store: name = %q", again.Roles[0].Views[0].Name)
	}
}

// A zero reenter_grace is allowed and is not replaced by the default: it seals
// a superseded tree at the first quiet after the notice is delivered
// (docs/design/readonly-view.md section 2).
func TestViewZeroReenterGraceStaysZero(t *testing.T) {
	t.Parallel()
	for _, zero := range []string{"0s", "0"} {
		views := appliedViews(t, viewManifest(
			viewTable(`name = "now"`, `remote = "r"`, `ref = "main"`, `reenter_grace = "`+zero+`"`),
			viewTable(`name = "dflt"`, `remote = "r"`, `ref = "main"`),
		))
		if views[0].ReenterGrace != 0 {
			t.Errorf("reenter_grace %q = %s, want 0", zero, views[0].ReenterGrace)
		}
		if views[1].ReenterGrace != 2*time.Minute {
			t.Errorf("omitted reenter_grace = %s, want the 2m default", views[1].ReenterGrace)
		}
	}
}
