package view

import (
	"os"
	"runtime"
	"testing"
)

// Fails only on a macOS GitHub runner, so the local pre-push gate and the
// Linux job stay green and the macOS job is the one that goes red.
func TestThrowawayTheMacOSJobCanFail(t *testing.T) {
	if runtime.GOOS == "darwin" && os.Getenv("GITHUB_ACTIONS") == "true" {
		t.Fatal("deliberate failure to show the macOS job goes red (marvel#623); reverted by the next commit")
	}
}
