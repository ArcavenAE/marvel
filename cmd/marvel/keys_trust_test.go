package main

import "testing"

// keys trust asks before it records, and --yes is the scripted form for an
// admin who already compared the fingerprint out of band (marvel#838).
func TestKeysTrustHasAYesFlagThatDefaultsOff(t *testing.T) {
	trust, _, err := keysCmd().Find([]string{"trust"})
	if err != nil || trust == nil || trust.Name() != "trust" {
		t.Fatalf("find keys trust: %v", err)
	}
	f := trust.Flags().Lookup("yes")
	if f == nil {
		t.Fatal("keys trust has no --yes flag")
	}
	if f.DefValue != "false" {
		t.Errorf("--yes default = %q, want false: the command must ask unless told not to", f.DefValue)
	}
}
