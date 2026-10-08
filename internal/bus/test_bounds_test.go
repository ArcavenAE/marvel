package bus

import (
	"os"
	"testing"
	"time"
)

// loadedBrokerWait is the least a test that starts a real nats-server may wait
// on any one step: a full -race run puts the broker and its clients behind
// every other test (marvel#493).
const loadedBrokerWait = 30 * time.Second

// TestMain widens the two RPC bounds for every test in the package, which all
// run a real broker behind the rest of the suite.
func TestMain(m *testing.M) {
	provisionRPC, structureRPC = loadedBrokerWait, loadedBrokerWait
	os.Exit(m.Run())
}

// A test that starts a real broker waits long enough for it on a loaded host.
// The shared supervisor helper, the provisioning RPC and the structure RPC
// each gave up at 5s or 10s, and each was seen to expire in the full suite.
func TestBrokerWaitsOutlastALoadedHost(t *testing.T) {
	s, _, _ := newTestSupervisor(t, "")
	for name, got := range map[string]time.Duration{
		"supervisor helper dial timeout": s.dialTimeout,
		"provisioning RPC":               provisionRPC,
		"structure RPC":                  structureRPC,
	} {
		if got < loadedBrokerWait {
			t.Errorf("%s = %v, want at least %v", name, got, loadedBrokerWait)
		}
	}
}

// SetDialTimeout is what a test in another package uses to widen the listener
// wait; it must take effect.
func TestSetDialTimeoutIsApplied(t *testing.T) {
	s, _, _ := newTestSupervisor(t, "")
	s.SetDialTimeout(42 * time.Second)
	if s.dialTimeout != 42*time.Second {
		t.Errorf("dialTimeout = %v after SetDialTimeout(42s)", s.dialTimeout)
	}
}
