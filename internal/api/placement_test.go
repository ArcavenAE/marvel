package api

import (
	"path/filepath"
	"strings"
	"testing"
)

const placementHead = "[workspace]\nname = \"aae\"\n%s\n[[team]]\nname = \"squad\"\n  [[team.role]]\n  name = \"w\"\n  replicas = 1\n    [team.role.runtime]\n    command = \"claude\"\n"

func placementManifest(t *testing.T, workspaceExtra string) (*Manifest, error) {
	t.Helper()
	return ParseManifestBytes([]byte(strings.Replace(placementHead, "%s", workspaceExtra, 1)))
}

// Section 5 of docs/design/workspace-sandbox-shape.md, ruling D2: the explicit
// workspace value wins, then the harness's recommendation, then localhost. The
// row that matters most is an explicit localhost against a container
// recommendation: the operator's word beats the default.
func TestResolvePlacementOrder(t *testing.T) {
	for name, tc := range map[string]struct {
		explicit, recommended, want Placement
	}{
		"nothing set runs on the host":                        {"", "", PlacementLocalhost},
		"a recommendation applies when nothing is set":        {"", PlacementContainer, PlacementContainer},
		"explicit localhost beats a container recommendation": {PlacementLocalhost, PlacementContainer, PlacementLocalhost},
		"explicit container with no recommendation":           {PlacementContainer, "", PlacementContainer},
		"explicit container beats a localhost recommendation": {PlacementContainer, PlacementLocalhost, PlacementContainer},
		"explicit localhost with no recommendation":           {PlacementLocalhost, "", PlacementLocalhost},
		"a localhost recommendation is the default anyway":    {"", PlacementLocalhost, PlacementLocalhost},
	} {
		got, err := ResolvePlacement(tc.explicit, tc.recommended)
		if err != nil {
			t.Errorf("%s: unexpected error %v", name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: ResolvePlacement(%q, %q) = %q, want %q", name, tc.explicit, tc.recommended, got, tc.want)
		}
	}
}

// An unknown value in either slot is an error, never a quiet fall through to a
// lower rung: a stored typo must not launch on a different place than written.
func TestResolvePlacementRefusesAnUnknownValue(t *testing.T) {
	for _, tc := range []struct{ explicit, recommended Placement }{
		{"docker", ""},
		{"Container", ""},
		{"docker", PlacementContainer},
		{"", "docker"},
		{PlacementLocalhost, "docker"},
	} {
		got, err := ResolvePlacement(tc.explicit, tc.recommended)
		if err == nil {
			t.Errorf("ResolvePlacement(%q, %q) = %q, want an error", tc.explicit, tc.recommended, got)
			continue
		}
		if got != "" {
			t.Errorf("ResolvePlacement(%q, %q) returned %q with an error, want empty", tc.explicit, tc.recommended, got)
		}
	}
}

func TestManifestPlacementAbsentAndListed(t *testing.T) {
	for name, tc := range map[string]struct {
		extra string
		want  Placement
	}{
		"absent":    {"", ""},
		"localhost": {`placement = "localhost"`, PlacementLocalhost},
		"container": {`placement = "container"`, PlacementContainer},
	} {
		m, err := placementManifest(t, tc.extra)
		if err != nil {
			t.Fatalf("toml %s: %v", name, err)
		}
		if got := Placement(m.Workspace.Placement); got != tc.want {
			t.Errorf("toml %s: placement = %q, want %q", name, got, tc.want)
		}
	}
	yamlSrc := func(extra string) string {
		return "workspace:\n  name: aae\n" + extra + "teams:\n  - name: squad\n    roles:\n      - name: w\n        replicas: 1\n        runtime:\n          command: claude\n"
	}
	for name, tc := range map[string]struct {
		extra string
		want  Placement
	}{
		"absent":    {"", ""},
		"localhost": {"  placement: localhost\n", PlacementLocalhost},
		"container": {"  placement: container\n", PlacementContainer},
	} {
		m, err := ParseManifestBytes([]byte(yamlSrc(tc.extra)))
		if err != nil {
			t.Fatalf("yaml %s: %v", name, err)
		}
		if got := Placement(m.Workspace.Placement); got != tc.want {
			t.Errorf("yaml %s: placement = %q, want %q", name, got, tc.want)
		}
	}
	if _, err := ParseManifestBytes([]byte(yamlSrc("  placement: docker\n"))); err == nil || !strings.Contains(err.Error(), "workspace.placement") {
		t.Errorf("yaml unknown value: err = %v, want a refusal naming workspace.placement", err)
	}
}

func TestManifestPlacementRefusesAnUnknownValue(t *testing.T) {
	for _, bad := range []string{"docker", "Container", "LOCALHOST", " container", "container ", "host", "none", ""} {
		_, err := placementManifest(t, `placement = "`+bad+`"`)
		if bad == "" {
			// An empty string is the same as no key: nothing to refuse.
			if err != nil {
				t.Errorf("empty placement refused: %v", err)
			}
			continue
		}
		if err == nil {
			t.Errorf("placement %q parsed, want a refusal", bad)
			continue
		}
		msg := err.Error()
		for _, want := range []string{"workspace.placement", `"localhost"`, `"container"`} {
			if !strings.Contains(msg, want) {
				t.Errorf("placement %q: error %q does not name %s", bad, msg, want)
			}
		}
	}
}

// An apply that names a placement stores it, an apply that names none leaves the
// stored one alone, and a later value replaces it. The store holds the explicit
// word only; resolution happens at launch, so an unset workspace stays unset.
func TestApplyPlacementStoresExplicitValueOnly(t *testing.T) {
	store := NewStore()
	apply := func(extra string) Placement {
		t.Helper()
		m, err := placementManifest(t, extra)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if err := m.Apply(store); err != nil {
			t.Fatalf("apply: %v", err)
		}
		w, err := store.GetWorkspace("aae")
		if err != nil {
			t.Fatal(err)
		}
		return w.Placement
	}
	if got := apply(""); got != "" {
		t.Fatalf("first apply with no key stored %q, want unset", got)
	}
	if got := apply(`placement = "localhost"`); got != PlacementLocalhost {
		t.Errorf("explicit localhost stored %q", got)
	}
	if got := apply(""); got != PlacementLocalhost {
		t.Errorf("an apply with no key stored %q, want the stored value left alone", got)
	}
	if got := apply(`placement = "container"`); got != PlacementContainer {
		t.Errorf("a new value stored %q, want container", got)
	}
}

// The create path stores the value too, and an apply that moves the root while
// naming no placement leaves the stored placement alone.
func TestApplyPlacementOnCreateAndAlongsideARootMove(t *testing.T) {
	store := NewStore()
	apply := func(extra string) Placement {
		t.Helper()
		m, err := placementManifest(t, extra)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if err := m.Apply(store); err != nil {
			t.Fatalf("apply: %v", err)
		}
		w, err := store.GetWorkspace("aae")
		if err != nil {
			t.Fatal(err)
		}
		return w.Placement
	}
	if got := apply(`placement = "container"`); got != PlacementContainer {
		t.Fatalf("the create apply stored %q, want container", got)
	}
	root := t.TempDir()
	if got := apply(`root = "` + root + `"`); got != PlacementContainer {
		t.Errorf("an apply that moved only the root stored placement %q, want container left alone", got)
	}
	w, _ := store.GetWorkspace("aae")
	if w.Root != root {
		t.Errorf("root = %q, want %q (the test must have entered the update path)", w.Root, root)
	}
}

func TestPlacementSurvivesAReopenOfTheStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt: %v", err)
	}
	if err := s1.CreateWorkspace(&Workspace{Name: "aae", Placement: PlacementLocalhost}); err != nil {
		t.Fatal(err)
	}
	if err := s1.CloseBolt(); err != nil {
		t.Fatal(err)
	}
	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	w, err := s2.GetWorkspace("aae")
	if err != nil {
		t.Fatal(err)
	}
	if w.Placement != PlacementLocalhost {
		t.Errorf("after reopen placement = %q, want localhost", w.Placement)
	}
}
