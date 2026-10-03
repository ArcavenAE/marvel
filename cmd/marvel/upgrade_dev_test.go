package main

import (
	"bytes"
	"strings"
	"testing"
)

// A dev build must not self-upgrade. The refusal comes before any install
// work, so this test touches neither the network nor the binary.
func TestUpgradeRefusesADevBuild(t *testing.T) {
	if version != "dev" {
		t.Skipf("build stamped %q; this checks the unstamped dev build", version)
	}
	cmd := upgradeCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--version", "alpha-20261002-232554-bc327df"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "dev build") {
		t.Fatalf("error = %v, want a dev build refusal", err)
	}
}
