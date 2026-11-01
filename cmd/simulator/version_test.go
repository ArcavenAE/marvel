package main

import "testing"

// marvel version reads this line to compare the simulator's build with its own.
func TestVersionLineNamesTheProgramVersionAndChannel(t *testing.T) {
	t.Parallel()
	got := versionLine("0.1.0-alpha.20261101.120000.abc1234", "alpha")
	want := "marvel-simulator 0.1.0-alpha.20261101.120000.abc1234 (alpha)"
	if got != want {
		t.Errorf("versionLine = %q, want %q", got, want)
	}
}
