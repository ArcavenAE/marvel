package api

import (
	"strings"
	"testing"
)

const imageHead = "[workspace]\nname = \"aae\"\n[[team]]\nname = \"squad\"\n  [[team.role]]\n  name = \"w\"\n  replicas = 1\n%s\n    [team.role.runtime]\n    command = \"pi\"\n"

func imageManifest(t *testing.T, roleExtra string) (*Manifest, error) {
	t.Helper()
	return ParseManifestBytes([]byte(strings.Replace(imageHead, "%s", roleExtra, 1)))
}

// container_image is a per-role key, in TOML and in YAML, carried to the applied
// role; absent means none.
func TestContainerImageIsARoleKeyCarriedToTheAppliedRole(t *testing.T) {
	for name, tc := range map[string]struct{ extra, want string }{
		"absent": {"", ""},
		"set":    {`  container_image = "registry.example/pi:1"`, "registry.example/pi:1"},
	} {
		m, err := imageManifest(t, tc.extra)
		if err != nil {
			t.Fatalf("toml %s: %v", name, err)
		}
		if got := m.Teams[0].Roles[0].ContainerImage; got != tc.want {
			t.Errorf("toml %s: container_image = %q, want %q", name, got, tc.want)
		}
		store := NewStore()
		if err := m.Apply(store); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		team, err := store.GetTeam("aae/squad")
		if err != nil {
			t.Fatal(err)
		}
		if got := team.Roles[0].ContainerImage; got != tc.want {
			t.Errorf("applied %s: ContainerImage = %q, want %q", name, got, tc.want)
		}
	}
	y := "workspace:\n  name: aae\nteams:\n  - name: squad\n    roles:\n      - name: w\n        replicas: 1\n        container_image: registry.example/pi:1\n        runtime:\n          command: pi\n"
	m, err := ParseManifestBytes([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Teams[0].Roles[0].ContainerImage; got != "registry.example/pi:1" {
		t.Errorf("yaml: container_image = %q", got)
	}
}

func TestValidContainerImage(t *testing.T) {
	for _, ok := range []string{"alpine", "registry.example/ns/img:1.2", "img@sha256:0123abcd", "a/b_c-d.e:tag+x"} {
		if !ValidContainerImage(ok) {
			t.Errorf("ValidContainerImage(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", " ", "a b", "-x", "--privileged", "a;b", "a\nb", "a\tb", "$(x)", "`x`", "a'b", "a\"b", "a|b", "a\\b", "ünï"} {
		if ValidContainerImage(bad) {
			t.Errorf("ValidContainerImage(%q) = true, want false", bad)
		}
	}
}

// A role whose image is not a plain reference is refused at parse, naming the
// role and the key, before anything is applied.
func TestManifestRefusesAContainerImageThatIsNotPlain(t *testing.T) {
	for _, bad := range []string{"a b", "-x", "a;b", "$(x)"} {
		_, err := imageManifest(t, `  container_image = "`+bad+`"`)
		if err == nil {
			t.Errorf("container_image %q parsed, want a refusal", bad)
			continue
		}
		if msg := err.Error(); !strings.Contains(msg, "container_image") || !strings.Contains(msg, `"w"`) {
			t.Errorf("container_image %q: error %q does not name the key and the role", bad, msg)
		}
	}
}
