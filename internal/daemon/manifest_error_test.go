package daemon

import (
	"strings"
	"testing"
)

// TestHandleApplyDoesNotDoublePrefixParseError guards the operator-facing
// error string on the only apply path. ParseManifestBytes already prefixes
// every validation failure with "parse manifest:", so wrapping it again in
// handleApply produced "parse manifest: parse manifest: <rule>". The rule
// still has to reach the operator; only the doubled prefix is a defect.
func TestHandleApplyDoesNotDoublePrefixParseError(t *testing.T) {
	// A YAML manifest with a validation error (replicas must be >= 1). The
	// masking fix (PR #101) already routes this to the real rule; this test
	// covers the formatting of that error, not the routing.
	const badYAML = `
workspace:
  name: doubled
teams:
  - name: crew
    roles:
      - name: crew
        replicas: 0
        runtime:
          command: sleep
`
	d := newHandlerDaemon(t)
	resp := applyManifest(t, d, badYAML)

	if resp.Error == "" {
		t.Fatal("expected a parse error for replicas: 0, got none")
	}
	if !strings.Contains(resp.Error, "replicas must be >= 1") {
		t.Errorf("error = %q, want it to name the broken rule", resp.Error)
	}
	if strings.Contains(resp.Error, "parse manifest: parse manifest:") {
		t.Errorf("error = %q, want no doubled %q prefix", resp.Error, "parse manifest:")
	}
	// The operator reads a YAML manifest; a TOML complaint would name the
	// wrong language (the masking regression this ticket also tracked).
	if strings.Contains(resp.Error, "toml") {
		t.Errorf("error = %q, want no TOML complaint about a YAML manifest", resp.Error)
	}
}
