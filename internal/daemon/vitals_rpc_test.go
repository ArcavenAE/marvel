package daemon

import (
	"encoding/json"
	"testing"

	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/hostvitals"
)

// vitalsSnapshot decodes a host.vitals answer, failing on an error response.
func vitalsSnapshot(t *testing.T, resp daemonrpc.Response) hostvitals.Snapshot {
	t.Helper()
	if resp.Error != "" {
		t.Fatalf("host.vitals answered an error: %s", resp.Error)
	}
	var snap hostvitals.Snapshot
	if err := json.Unmarshal(resp.Result, &snap); err != nil {
		t.Fatalf("host.vitals result is not a snapshot: %v\n%s", err, resp.Result)
	}
	return snap
}

// host.vitals answers from the sampler's memory: the latest reading and its
// threshold states, with no new read of the host.
func TestHostVitalsAnswersFromTheSampler(t *testing.T) {
	reads := 0
	d := &Daemon{vitals: hostvitals.New(true, func() hostvitals.Reading {
		reads++
		return hostvitals.Reading{SwapOK: true, SwapUsed: 60, SwapTotal: 100}
	}, nil)}
	d.vitals.Sample()

	snap := vitalsSnapshot(t, d.handleHostVitals())

	if !snap.Enabled || !snap.Sampled || snap.At == nil {
		t.Errorf("snapshot = %+v, want enabled and sampled with a time", snap)
	}
	if !snap.Reading.SwapOK || snap.Reading.SwapUsed != 60 {
		t.Errorf("reading = %+v, want the sampled swap figures", snap.Reading)
	}
	var swap string
	for _, th := range snap.Thresholds {
		if th.Name == "swap used" {
			swap = th.State
		}
	}
	if swap != "over" {
		t.Errorf("swap used state = %q, want over", swap)
	}
	if reads != 1 {
		t.Errorf("the host was read %d times, want 1: the handler must not read it", reads)
	}
}

// A daemon whose cluster did not turn host vitals on answers a snapshot that
// says so. It is not an error: the question has an answer.
func TestHostVitalsOffIsAnAnswerNotAnError(t *testing.T) {
	d := &Daemon{vitals: hostvitals.New(false, func() hostvitals.Reading { return hostvitals.Reading{} }, nil)}
	snap := vitalsSnapshot(t, d.handleHostVitals())
	if snap.Enabled || snap.Sampled {
		t.Errorf("snapshot = %+v, want enabled false", snap)
	}
}

// A daemon that never built a sampler (a test daemon, or one that failed to
// load its config) answers the same off snapshot and does not panic.
func TestHostVitalsWithNoSamplerIsOff(t *testing.T) {
	d := &Daemon{}
	snap := vitalsSnapshot(t, d.handleHostVitals())
	if snap.Enabled {
		t.Errorf("snapshot = %+v, want enabled false", snap)
	}
}
