package daemon

import (
	"strings"
	"testing"
)

// A model on a harness marvel cannot hand it to is refused at apply, before
// anything is stored (docs/design/role-runtime-model.md).
func TestHandleApplyRefusesModelOnGeneric(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := applyManifest(t, d, `
workspace:
  name: modelrefuse
teams:
  - name: crew
    roles:
      - name: w
        replicas: 1
        runtime:
          image: generic
          command: sleep
          args: ["300"]
          model: sonnet
`)
	if resp.Error == "" {
		t.Fatal("apply succeeded with a model on a generic role")
	}
	if !strings.Contains(resp.Error, "generic") {
		t.Errorf("error %q does not name the adapter", resp.Error)
	}
	if _, err := d.store.GetTeam("modelrefuse/crew"); err == nil {
		t.Error("a refused apply stored its team")
	}
}
