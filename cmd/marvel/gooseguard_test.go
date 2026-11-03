package main

import (
	"bytes"
	"testing"

	"github.com/arcavenae/marvel/internal/goosewatch"
)

func TestGooseGuardIsHidden(t *testing.T) {
	t.Parallel()
	if !newGooseGuardCmd().Hidden {
		t.Error("goose-guard is internal and must be hidden")
	}
}

// The guard refuses to run without its three flags, before it reads any pipe.
func TestGooseGuardNeedsItsFlags(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		nil,
		{"--grace", "1s", "--stderr-file", "f"},
		{"--grace", "1s", "--arg", "a"},
		{"--stderr-file", "f", "--arg", "a"},
	} {
		cmd := newGooseGuardCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(args)
		if got := exitCodeFor(cmd.Execute()); got != goosewatch.ExitUsage {
			t.Errorf("args %v: exit %d, want %d", args, got, goosewatch.ExitUsage)
		}
	}
}
