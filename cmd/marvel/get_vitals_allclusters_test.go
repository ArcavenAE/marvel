package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/hostvitals"
)

// `marvel get vitals --all-clusters` reads host.vitals from every cluster in
// the client config, in parallel, each under the fan-out timer. A cluster that
// cannot be read is a gap line that names why, never a row of zeros.

// The one read method a vitals fan-out may call is allowed by the daemon's
// method table.
func TestVitalsFanOutMethodIsAnAllowedRead(t *testing.T) {
	found := false
	for _, m := range fanOutMethods {
		if m == "host.vitals" {
			found = true
		}
	}
	if !found {
		t.Errorf("host.vitals is not in the fan-out read allowlist %v", fanOutMethods)
	}
}

// Every cluster is asked once, with host.vitals and nothing else, and each
// cluster's reading shows under its own name.
func TestGetVitalsAllClustersReadsEveryCluster(t *testing.T) {
	sockA, methodsA := vitalsFakeDaemon(t, snapshotResponse(t, sampledSnapshot()))
	quiet := sampledSnapshot()
	quiet.Thresholds = quiet.Thresholds[:2]
	quiet.Top, quiet.Orphans = nil, nil
	sockB, methodsB := vitalsFakeDaemon(t, snapshotResponse(t, quiet))
	clusterConfig(t, config.Cluster{Name: "a", Socket: sockA}, config.Cluster{Name: "b", Socket: sockB})

	code, out, errOut := runFanGet(t, "get", "vitals", "--all-clusters")
	if code != 0 {
		t.Fatalf("exit %d, want 0; stderr %q", code, errOut)
	}
	for _, want := range []string{"a", "b", "swap used"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	// The process rows only cluster a reported appear once, under a.
	if n := strings.Count(out, "Acme Helper"); n == 0 {
		t.Errorf("cluster a's top processes are missing:\n%s", out)
	}
	for name, got := range map[string][]string{"a": methodsA(), "b": methodsB()} {
		if len(got) != 1 || got[0] != "host.vitals" {
			t.Errorf("cluster %s saw methods %v, want exactly [host.vitals]", name, got)
		}
	}
}

// A cluster that does not answer is shown as a gap with its outcome, the
// answering cluster is still shown, and the exit says some did not answer.
func TestGetVitalsAllClustersShowsAnUnreadableClusterAsAGap(t *testing.T) {
	fastTimer(t, 500*time.Millisecond)
	sockA, _ := vitalsFakeDaemon(t, snapshotResponse(t, sampledSnapshot()))
	h := startFake(t, fakeHung, nil)
	clusterConfig(t, config.Cluster{Name: "a", Socket: sockA}, config.Cluster{Name: "h", Socket: h.sock})

	code, out, errOut := runFanGet(t, "get", "vitals", "--all-clusters")
	if code != 10 {
		t.Errorf("exit = %d, want 10 (some answered, some did not); stderr %q", code, errOut)
	}
	if !strings.Contains(out, "swap used") {
		t.Errorf("the answering cluster's reading is missing:\n%s", out)
	}
	// The gap is its own line in stdout, begun by the cluster's name and the
	// outcome, and no vitals reading follows it for that cluster.
	const gapPrefix = "cluster h: GAP " + string(outcomeNoAnswer) + ":"
	lines := strings.Split(out, "\n")
	gapAt := -1
	for i, l := range lines {
		if strings.HasPrefix(l, gapPrefix) {
			gapAt = i
		}
	}
	if gapAt < 0 {
		t.Fatalf("no stdout line begins %q:\nstdout:\n%s\nstderr:\n%s", gapPrefix, out, errOut)
	}
	for _, l := range lines[gapAt+1:] {
		if strings.HasPrefix(l, "cluster ") {
			break
		}
		if strings.TrimSpace(l) != "" {
			t.Errorf("a reading follows the gap line for cluster h: %q\nstdout:\n%s", l, out)
		}
	}
}

// A cluster whose host_vitals switch is off is not a gap and not zeros: the
// reading says it is off, as the one-cluster command does.
func TestGetVitalsAllClustersShowsAnOffClusterAsOff(t *testing.T) {
	sockA, _ := vitalsFakeDaemon(t, snapshotResponse(t, sampledSnapshot()))
	sockB, _ := vitalsFakeDaemon(t, snapshotResponse(t, hostvitals.Snapshot{}))
	clusterConfig(t, config.Cluster{Name: "a", Socket: sockA}, config.Cluster{Name: "b", Socket: sockB})

	code, out, errOut := runFanGet(t, "get", "vitals", "--all-clusters")
	if code != 0 {
		t.Fatalf("exit %d, want 0; stderr %q", code, errOut)
	}
	if !strings.Contains(out, "off") {
		t.Errorf("cluster b's off state is not shown:\n%s", out)
	}
}

// -o json prints one document: an outcome record per cluster in config order,
// and a snapshot only for a cluster that answered.
func TestGetVitalsAllClustersJSON(t *testing.T) {
	fastTimer(t, 500*time.Millisecond)
	sockA, _ := vitalsFakeDaemon(t, snapshotResponse(t, sampledSnapshot()))
	h := startFake(t, fakeHung, nil)
	clusterConfig(t, config.Cluster{Name: "a", Socket: sockA}, config.Cluster{Name: "h", Socket: h.sock})

	code, out, errOut := runFanGet(t, "get", "vitals", "--all-clusters", "-o", "json")
	if code != 10 {
		t.Fatalf("exit = %d, want 10; stderr %q", code, errOut)
	}
	var doc struct {
		Clusters []clusterRecord `json:"clusters"`
		Vitals   []struct {
			Cluster  string              `json:"cluster"`
			Snapshot hostvitals.Snapshot `json:"snapshot"`
		} `json:"vitals"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not the document (%v):\n%s", err, out)
	}
	if len(doc.Clusters) != 2 || doc.Clusters[0].Cluster != "a" || doc.Clusters[0].Outcome != outcomeAnswered ||
		doc.Clusters[1].Cluster != "h" || doc.Clusters[1].Outcome != outcomeNoAnswer {
		t.Errorf("cluster records = %+v, want a answered then h %s", doc.Clusters, outcomeNoAnswer)
	}
	if len(doc.Vitals) != 1 || doc.Vitals[0].Cluster != "a" || !doc.Vitals[0].Snapshot.Sampled {
		t.Errorf("vitals = %+v, want one sampled snapshot for a and none for h", doc.Vitals)
	}
}

// The dials run in parallel: three hung clusters finish under twice the timer.
func TestGetVitalsAllClustersIsParallelAndBounded(t *testing.T) {
	timer := 600 * time.Millisecond
	fastTimer(t, timer)
	var entries []config.Cluster
	for _, n := range []string{"a", "b", "c"} {
		entries = append(entries, config.Cluster{Name: n, Socket: startFake(t, fakeHung, nil).sock})
	}
	clusterConfig(t, entries...)
	start := time.Now()
	code, _, errOut := runFanGet(t, "get", "vitals", "--all-clusters")
	d := time.Since(start)
	if d < timer {
		t.Errorf("returned after %v, before the %v timer: the clusters were not dialled", d, timer)
	}
	if d >= 2*timer {
		t.Errorf("three hung clusters took %v, want under %v", d, 2*timer)
	}
	if want := outcomeExit[outcomeNoAnswer]; code != want {
		t.Errorf("exit = %d, want %d (every cluster gave no answer); stderr %q", code, want, errOut)
	}
}

// Refusals come before any dial: no watch, no format but json, and the flag
// rules the sessions fan-out already has.
func TestGetVitalsAllClustersFlagRules(t *testing.T) {
	// Positive control: the plain form is accepted, so the refusals below are
	// about the flags and not about the verb.
	ok, _ := vitalsFakeDaemon(t, snapshotResponse(t, sampledSnapshot()))
	clusterConfig(t, config.Cluster{Name: "ok", Socket: ok})
	if code, _, stderr := runFanGet(t, "get", "vitals", "--all-clusters"); code != 0 {
		t.Fatalf("get vitals --all-clusters: exit = %d, want 0\n%s", code, stderr)
	}
	a := startFake(t, fakeAnswers, nil)
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock})
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"watch", []string{"get", "vitals", "--all-clusters", "--watch"}, "get vitals does not support --watch"},
		{"yaml", []string{"get", "vitals", "--all-clusters", "-o", "yaml"}, "-o yaml is not supported"},
		{"columns", []string{"get", "vitals", "--all-clusters", "--columns", "key"}, "--columns applies to get sessions only"},
		{"cluster with all", []string{"get", "vitals", "--all-clusters", "--cluster", "a"}, "--all-clusters and --cluster cannot be combined"},
		{"socket with all", []string{"get", "vitals", "--all-clusters", "--socket", "/nonexistent"}, "--socket and --identity name one daemon"},
	}
	for _, c := range cases {
		code, _, stderr := runFanGet(t, c.args...)
		if code != 1 {
			t.Errorf("%s: exit = %d, want 1\n%s", c.name, code, stderr)
		}
		if !strings.Contains(stderr, c.want) {
			t.Errorf("%s: stderr lacks the intended refusal %q:\n%s", c.name, c.want, stderr)
		}
	}
	if a.conns() != 0 {
		t.Errorf("%d connections made by refused commands, want 0", a.conns())
	}
}

// Other resources still refuse --all-clusters: the guard widens to vitals
// only. The control is that vitals itself is read in the same config.
func TestAllClustersStillRefusedForOtherReads(t *testing.T) {
	sock, _ := vitalsFakeDaemon(t, snapshotResponse(t, sampledSnapshot()))
	clusterConfig(t, config.Cluster{Name: "a", Socket: sock})
	if code, _, stderr := runFanGet(t, "get", "vitals", "--all-clusters"); code != 0 {
		t.Fatalf("control, get vitals --all-clusters: exit = %d, want 0\n%s", code, stderr)
	}
	for _, res := range []string{"teams", "workspaces", "endpoints", "policies"} {
		code, _, stderr := runFanGet(t, "get", res, "--all-clusters")
		if code != 1 {
			t.Errorf("get %s --all-clusters: exit = %d, want 1\n%s", res, code, stderr)
		}
		if want := "apply to get sessions and get vitals only"; !strings.Contains(stderr, want) {
			t.Errorf("get %s --all-clusters: stderr lacks the intended refusal %q:\n%s", res, want, stderr)
		}
	}
}
