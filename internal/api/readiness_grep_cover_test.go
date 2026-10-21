package api

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// marryEntryPoints are the functions the shared vectors call.
var marryEntryPoints = []string{"Marry", "MarryAddress", "AnswerTake", "RecheckBeforeSend"}

// The trigger row's grep reads every file of the marry source, so a send path
// added to a helper the step reaches is caught, not only one in
// readiness_marry.go.
func TestMarryGrepReadsEveryMarrySourceFile(t *testing.T) {
	t.Parallel()
	want := marrySourceReach(t).files
	// Positive controls: the reach finds the marry file itself, and follows a
	// reference out of it (MarryAddress uses agentAddress).
	for _, f := range []string{"internal/api/readiness_marry.go", "internal/api/readiness_address.go"} {
		if !slices.Contains(want, f) {
			t.Fatalf("the reach found %q, which misses %s", want, f)
		}
	}
	var grepped []string
	for _, v := range loadMarryVectors(t).Vectors {
		for _, st := range v.Steps {
			if st.Op == "grep" {
				grepped = append(grepped, st.File)
			}
		}
	}
	for _, f := range want {
		if !slices.Contains(grepped, f) {
			t.Errorf("the marry source file %s has no grep step", f)
		}
	}
	if _, err := os.Stat(filepath.Join("..", "..", "internal", "api", "readiness_marry.go")); err != nil {
		t.Fatalf("the grep paths do not resolve from here: %v", err)
	}
}
