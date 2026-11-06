package main

import (
	"os"
	"testing"
)

// The statusline payload carries both a model id and a display name. The
// forwarder reads the id for the observed-model check.
func TestCtxForwardSendsModelID(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/statusline-2.1.226-empty.json")
	if err != nil {
		t.Fatal(err)
	}
	if got := statuslineModelID(raw); got != "claude-opus-5[1m]" {
		t.Errorf("model id = %q, want claude-opus-5[1m]", got)
	}
}
