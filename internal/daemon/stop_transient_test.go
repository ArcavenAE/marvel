package daemon

import (
	"encoding/json"
	"testing"
)

// `marvel stop --keep-bus` tells the operator what a detach costs: the daemon
// reports how many transient credentials it holds, so the CLI can say they will
// be dropped (aae-orc-4h9xp, section 6). It reports; it never refuses.
func TestStopReportCountsTheTransientCredentialsTheDetachDrops(t *testing.T) {
	d := newHandlerDaemon(t)
	read := func() *int {
		resp := d.stopReport(false, "detach")
		if resp.Error != "" {
			t.Fatalf("stop was refused: %s", resp.Error)
		}
		var res StopResult
		if err := json.Unmarshal(resp.Result, &res); err != nil {
			t.Fatal(err)
		}
		return res.TransientCredentials
	}
	if n := read(); n == nil || *n != 0 {
		t.Errorf("a daemon holding none reported %v, want 0 (present, so the CLI can tell it from an older daemon)", n)
	}
	putDummy(t, d, "bus/leaf", handoffCanary)
	putDummy(t, d, "bus/hub", handoffCanary+"2")
	if n := read(); n == nil || *n != 2 {
		t.Errorf("a daemon holding two reported %v, want 2", n)
	}
	body, _ := json.Marshal(d.stopReport(false, "detach"))
	if string(body) == "" || json.Valid(body) == false {
		t.Fatal("the stop report is not JSON")
	}
	if contains(string(body), handoffCanary) {
		t.Error("the stop report carries a credential value")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
