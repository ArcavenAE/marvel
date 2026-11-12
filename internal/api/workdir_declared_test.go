package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckDeclaredRoot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "afile")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "nope")
	cases := []struct {
		name, root, want string // want empty means no error
	}{
		{"none declared", "", ""},
		{"existing directory", dir, ""},
		{"missing directory", missing, "does not exist on this host"},
		{"a file", file, "not a directory"},
		{"relative root is left to ValidateWorkDirs", "rel/root", ""},
		{"tilde root is left to ValidateWorkDirs", "~/proj", ""},
	}
	for _, tc := range cases {
		err := CheckDeclaredRoot(tc.root)
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: error %v, want none", tc.name, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: no error, want one containing %q", tc.name, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), tc.root) {
			t.Errorf("%s: error %q, want it to name %q and contain %q", tc.name, err, tc.root, tc.want)
		}
	}
}

func TestRolesPlacedFromRoot(t *testing.T) {
	t.Parallel()
	m := &Manifest{Teams: []ManifestTeam{
		{Name: "a", Roles: []ManifestRole{{Name: "one"}, {Name: "two", WorkDir: "/x"}}},
		{Name: "b", WorkDir: "/y", Roles: []ManifestRole{{Name: "three"}}},
	}}
	got := strings.Join(m.RolesPlacedFromRoot(), ",")
	if got != "a/one" {
		t.Errorf("RolesPlacedFromRoot = %q, want a/one", got)
	}
}
