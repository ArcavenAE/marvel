package runtime

import (
	"strings"
	"testing"
)

// TestProjectionFor exercises the projection surface each adapter declares.
// claude and forestage read a Claude Code settings fragment; codex,
// opencode, and generic have none and must report Supported=false so the
// manager logs the policy as advisory rather than dropping it.
func TestProjectionFor(t *testing.T) {
	t.Parallel()
	const dir = "/var/run/marvel/policies"

	tests := []struct {
		name          string
		adapter       Adapter
		wantSupported bool
		wantPath      string
	}{
		{"claude", &Claude{}, true, "/var/run/marvel/policies/acme-squad-worker-g1-0.settings.json"},
		{"forestage", &Forestage{}, true, "/var/run/marvel/policies/acme-squad-worker-g1-0.settings.json"},
		{"codex", &Codex{}, false, ""},
		{"opencode", &OpenCode{}, false, ""},
		{"generic", &Generic{}, false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.adapter.ProjectionFor(testContext(), dir)
			if got.Supported != tt.wantSupported {
				t.Fatalf("Supported = %v, want %v", got.Supported, tt.wantSupported)
			}
			if got.Path != tt.wantPath {
				t.Errorf("Path = %q, want %q", got.Path, tt.wantPath)
			}
		})
	}
}

// TestPrepareInjectsProjectionFlag confirms the supporting adapters point
// their harness at the projected file when the manager set the path, and
// place the flag correctly (claude top-level; forestage inside the "--"
// passthrough).
func TestPrepareInjectsProjectionFlag(t *testing.T) {
	t.Parallel()
	const path = "/var/run/marvel/policies/acme-squad-worker-g1-0.settings.json"

	t.Run("claude top-level", func(t *testing.T) {
		t.Parallel()
		ctx := testContext()
		ctx.Session.Runtime.Command = "claude"
		ctx.PolicyProjectionPath = path
		res, err := (&Claude{}).Prepare(ctx)
		if err != nil {
			t.Fatalf("prepare: %v", err)
		}
		if !strings.Contains(res.Command, "--settings "+path) {
			t.Errorf("command missing --settings %s:\n%s", path, res.Command)
		}
	})

	t.Run("forestage passthrough after --", func(t *testing.T) {
		t.Parallel()
		ctx := testContext()
		ctx.PolicyProjectionPath = path
		res, err := (&Forestage{}).Prepare(ctx)
		if err != nil {
			t.Fatalf("prepare: %v", err)
		}
		sep := strings.Index(res.Command, " -- ")
		if sep < 0 {
			t.Fatalf("no -- passthrough separator in:\n%s", res.Command)
		}
		flag := strings.Index(res.Command, "--settings "+path)
		if flag < 0 {
			t.Fatalf("command missing --settings %s:\n%s", path, res.Command)
		}
		if flag < sep {
			t.Errorf("--settings appears before -- separator (should be passthrough):\n%s", res.Command)
		}
	})

	t.Run("no path means no flag", func(t *testing.T) {
		t.Parallel()
		ctx := testContext()
		ctx.Session.Runtime.Command = "claude"
		res, err := (&Claude{}).Prepare(ctx)
		if err != nil {
			t.Fatalf("prepare: %v", err)
		}
		if strings.Contains(res.Command, "--settings") {
			t.Errorf("unexpected --settings with no projection path:\n%s", res.Command)
		}
	})
}
