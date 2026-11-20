//go:build darwin || linux

package procargs

import (
	"io"
	"os"
	"testing"
)

// helperEnv marks the test binary when it runs as the process a test reads.
const helperEnv = "PROCARGS_TEST_HOLD"

// TestHoldUntilStdinCloses is not a test. A test starts this binary again with
// helperEnv set, and the copy blocks here until its stdin closes, so the
// parent has a live process to read. A system shell is no good for this: macOS
// withholds the environment of a protected system binary from kern.procargs2,
// so the process read has to be one this build made.
func TestHoldUntilStdinCloses(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		t.Skip("helper process for TestReadMarker*, not a test")
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}
