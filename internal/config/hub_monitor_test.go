package config

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// hub_monitor and hub_expected_leafs sit on the bus entry, beside hub
// (aae-orc-stqn9). The names are invented.
func TestHubMonitorAndExpectedLeafsResolve(t *testing.T) {
	t.Parallel()
	var parsed Bus
	src := "managed: true\nlisten: 127.0.0.1:4222\nhub_monitor: 127.0.0.1:8442\nhub_expected_leafs:\n  - alpha\n  - bravo\n"
	if err := yaml.Unmarshal([]byte(src), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := ValidateBus("alpha", &parsed); err != nil {
		t.Fatalf("a loopback monitor and a leaf list are refused: %v", err)
	}
	r := parsed.Resolve("/state")
	if r.HubMonitor != "127.0.0.1:8442" {
		t.Errorf("HubMonitor = %q, want 127.0.0.1:8442", r.HubMonitor)
	}
	if want := []string{"alpha", "bravo"}; !reflect.DeepEqual(r.HubExpectedLeafs, want) {
		t.Errorf("HubExpectedLeafs = %v, want %v", r.HubExpectedLeafs, want)
	}
}

// The monitor needs no credential and is loopback only, so a non-loopback
// address or a malformed one is refused at validation.
func TestHubMonitorMustBeALoopbackHostPort(t *testing.T) {
	t.Parallel()
	for name, addr := range map[string]string{
		"a host name":     "hub.example:8442",
		"a lan address":   "192.0.2.10:8442",
		"no port":         "127.0.0.1",
		"a url":           "http://127.0.0.1:8442",
		"a port too high": "127.0.0.1:70000",
	} {
		err := ValidateBus("alpha", &Bus{Managed: true, Listen: "127.0.0.1:4222", HubMonitor: addr})
		if err == nil {
			t.Errorf("%s: %q was accepted", name, addr)
		} else if !strings.Contains(err.Error(), "hub_monitor") {
			t.Errorf("%s: error %q does not name hub_monitor", name, err)
		}
	}
	for _, ok := range []string{"127.0.0.1:8442", "localhost:8442", "[::1]:8442"} {
		if err := ValidateBus("alpha", &Bus{Managed: true, Listen: "127.0.0.1:4222", HubMonitor: ok}); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
}

// Each expected leaf is a cluster name, so an empty entry or one with a space
// is refused; an absent list and an empty list both resolve to no entries.
func TestHubExpectedLeafsEntriesAreClusterNames(t *testing.T) {
	t.Parallel()
	for name, list := range map[string][]string{"empty entry": {"alpha", ""}, "a space": {"alpha bravo"}, "a dot": {"alpha.bravo"}} {
		if err := ValidateBus("alpha", &Bus{Managed: true, Listen: "127.0.0.1:4222", HubExpectedLeafs: list}); err == nil {
			t.Errorf("%s: %q was accepted", name, list)
		}
	}
	for name, b := range map[string]*Bus{
		"absent": {Managed: true, Listen: "127.0.0.1:4222"},
		"empty":  {Managed: true, Listen: "127.0.0.1:4222", HubExpectedLeafs: []string{}},
	} {
		if got := b.Resolve("/state").HubExpectedLeafs; len(got) != 0 {
			t.Errorf("%s: resolved to %v, want none", name, got)
		}
	}
}

// 127.0.0.2 is loopback; the port edges are 1 and 65535.
func TestHubMonitorLoopbackAndPortEdges(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"127.0.0.2:1", "127.0.0.1:65535"} {
		if err := ValidateBus("alpha", &Bus{Managed: true, Listen: "127.0.0.1:4222", HubMonitor: ok}); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"127.0.0.1:0", "127.0.0.1:65536"} {
		if err := ValidateBus("alpha", &Bus{Managed: true, Listen: "127.0.0.1:4222", HubMonitor: bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
