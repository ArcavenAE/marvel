package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// An example manifest that names a program by a relative path must name one the
// pre-flight finds. marvel work anchors a workspace at the directory of the
// manifest file (workspaceRootFor), and the pre-flight resolves a relative
// command against that anchor, so "bin/simulator" in examples/demo.toml looks
// for examples/bin/simulator and refuses the README Quick Start (marvel#1062).
// The built simulator sits at <repo>/bin/simulator, which the examples reach as
// "../bin/simulator", the form examples/tour.toml already uses. A relative
// script resolves from the same directory, so it reaches <repo>/scripts as
// "../scripts/<name>" and must name a file that exists there.
func TestExampleManifestRelativePathsReachTheirTargets(t *testing.T) {
	repo := t.TempDir()
	wantBin := filepath.Join(repo, "bin", "simulator")
	if err := os.MkdirAll(filepath.Dir(wantBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wantBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	paths, err := filepath.Glob(filepath.Join("..", "..", "examples", "*"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no example manifests found: %v", err)
	}
	checked := 0
	for _, src := range paths {
		ext := filepath.Ext(src)
		if ext != ".toml" && ext != ".yaml" {
			continue
		}
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		m, err := api.ParseManifestBytes(data)
		if err != nil {
			t.Errorf("%s does not parse as a manifest, so its commands and scripts were not checked: %v", filepath.Base(src), err)
			continue
		}
		// Place the manifest where it sits in the repository, one level below
		// the root, and anchor it the way marvel work does.
		placed := filepath.Join(repo, "examples", filepath.Base(src))
		root := workspaceRootFor(placed, m.Workspace.Root)
		for _, tm := range m.Teams {
			for _, r := range tm.Roles {
				if sc := r.Runtime.Script; sc != "" && !filepath.IsAbs(sc) && r.WorkDir == "" && tm.WorkDir == "" {
					checked++
					realRoot := workspaceRootFor(src, m.Workspace.Root)
					if _, err := os.Stat(filepath.Join(realRoot, sc)); err != nil {
						t.Errorf("%s team %s role %s: script %q does not resolve from the manifest's directory: %v", filepath.Base(src), tm.Name, r.Name, sc, err)
					}
				}
				cmd := strings.Fields(r.Runtime.Command)
				if len(cmd) == 0 || filepath.IsAbs(cmd[0]) || !strings.ContainsRune(cmd[0], '/') {
					continue
				}
				if r.WorkDir != "" || tm.WorkDir != "" {
					continue // a declared workdir moves the anchor; these examples declare none
				}
				checked++
				if got := filepath.Join(root, cmd[0]); got != wantBin {
					t.Errorf("%s team %s role %s: command %q resolves to %s, want %s", filepath.Base(src), tm.Name, r.Name, cmd[0], got, wantBin)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no example role names a relative program; the test checked nothing")
	}
}
