package daemon

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/gittest"
)

const trustedFoldersManifest = `
workspace:
  name: trust-ws
  trusted_folders:
    - %s
teams:
  - name: crew
    roles:
      - name: crew
        replicas: 1
        runtime:
          command: sleep
          args: ["300"]
`

// apply refuses a trusted folder the daemon cannot see, before anything is
// stored, and says which key.
func TestApplyRefusesAMissingTrustedFolder(t *testing.T) {
	d := newHandlerDaemon(t)
	missing := filepath.Join(t.TempDir(), "nope")
	resp := applyManifest(t, d, strings.Replace(trustedFoldersManifest, "%s", missing, 1))
	if resp.Error == "" || !strings.Contains(resp.Error, "trusted_folders") {
		t.Fatalf("apply error = %q, want a refusal naming trusted_folders", resp.Error)
	}
	if _, err := d.store.GetWorkspace("trust-ws"); err == nil {
		t.Error("a refused apply still created the workspace")
	}
}

// A listed git top-level is stored on the workspace, which is where the seed
// step reads it.
func TestApplyStoresTheTrustedFolders(t *testing.T) {
	d := newHandlerDaemon(t)
	real := gittest.Repo(t, filepath.Join(t.TempDir(), "aae-orc"))
	if resp := applyManifest(t, d, strings.Replace(trustedFoldersManifest, "%s", real, 1)); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	ws, err := d.store.GetWorkspace("trust-ws")
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.TrustedFolders) != 1 || ws.TrustedFolders[0] != real {
		t.Errorf("stored trusted folders = %v, want [%s]", ws.TrustedFolders, real)
	}
}
