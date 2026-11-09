package jetstream

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// brokerChildMode is set only for the child binary of
// TestMissingBrokerFailsUnderCIAndSkipsOtherwise. It is read at init for the
// reason ciRun is: the test guard removes it before the tests run.
var brokerChildMode = os.Getenv("JETSTREAM_TEST_CHILD")

// TestChildNeedsABroker runs only as a child of
// TestMissingBrokerFailsUnderCIAndSkipsOtherwise, with no nats-server on PATH.
func TestChildNeedsABroker(t *testing.T) {
	if brokerChildMode != "missing-broker" {
		t.Skip("child only")
	}
	newScratchBroker(t)
}

// CI turns a missing nats-server into a failure and not a quiet skip, and the
// guard that scrubs the environment does not change that. The child is this test
// binary with an empty directory for PATH. Without CI it must skip, which is the
// control that the failure comes from CI and not from the missing binary.
func TestMissingBrokerFailsUnderCIAndSkipsOtherwise(t *testing.T) {
	run := func(ci bool) (string, error) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestChildNeedsABroker$", "-test.v")
		cmd.Env = []string{"JETSTREAM_TEST_CHILD=missing-broker", "HOME=" + t.TempDir(), "PATH=" + t.TempDir(), "TMPDIR=" + os.TempDir()}
		if ci {
			cmd.Env = append(cmd.Env, "CI=true")
		}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := run(true)
	if err == nil || !strings.Contains(out, "nats-server not on PATH under CI") {
		t.Errorf("a CI run with no nats-server did not fail (err %v):\n%s", err, out)
	}
	out, err = run(false)
	if err != nil || !strings.Contains(out, "--- SKIP: TestChildNeedsABroker") {
		t.Errorf("a run without CI and without nats-server did not skip (err %v):\n%s", err, out)
	}
}
