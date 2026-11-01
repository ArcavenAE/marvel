package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// Given only an install, the shipped tour applies: the pre-flight finds each
// seat's program on PATH, and no seat names a model runtime that needs a
// login (marvel#1175). The install is a PATH holding marvel-simulator and
// nothing from the source tree.
func TestTourPassesThePreflightWithOnlyAnInstalledSimulator(t *testing.T) {
	for _, f := range []string{"tour.toml", "tour.yaml"} {
		t.Run(f, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "examples", f))
			if err != nil {
				t.Fatal(err)
			}
			m, err := api.ParseManifestBytes(data)
			if err != nil {
				t.Fatal(err)
			}
			for _, tm := range m.Teams {
				for _, r := range tm.Roles {
					if !strings.HasPrefix(r.Runtime.Command, "marvel-simulator") || r.Runtime.Image != "simulator" {
						t.Errorf("role %s runs %q on image %q, want the installed simulator", r.Name, r.Runtime.Command, r.Runtime.Image)
					}
				}
			}

			bin := t.TempDir()
			if err := os.WriteFile(filepath.Join(bin, "marvel-simulator"), []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
			if _, err := m.ValidateRuntimes(); err != nil {
				t.Errorf("the pre-flight refuses the tour with marvel-simulator on PATH: %v", err)
			}

			t.Setenv("PATH", t.TempDir())
			if _, err := m.ValidateRuntimes(); err == nil {
				t.Error("the pre-flight accepts the tour with no marvel-simulator on PATH, so the test proves nothing")
			}
		})
	}
}
