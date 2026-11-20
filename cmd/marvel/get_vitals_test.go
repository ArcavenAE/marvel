package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/hostvitals"
)

// vitalsFakeDaemon answers host.vitals with resp and anything else with an
// error, and records the methods it was sent. The socket lives in a short
// directory: a unix socket path is capped near 104 bytes.
func vitalsFakeDaemon(t *testing.T, resp daemonrpc.Response) (socket string, methods func() []string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "gv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket = filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var (
		mu  sync.Mutex
		got []string
	)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var req daemonrpc.Request
			if err := json.NewDecoder(c).Decode(&req); err == nil {
				mu.Lock()
				got = append(got, req.Method)
				mu.Unlock()
				if req.Method == "host.vitals" {
					_ = json.NewEncoder(c).Encode(resp)
				} else {
					_ = json.NewEncoder(c).Encode(daemonrpc.Response{Error: "unexpected method " + req.Method})
				}
			}
			_ = c.Close()
		}
	}()
	return socket, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), got...)
	}
}

// snapshotResponse wraps a snapshot as the daemon's answer.
func snapshotResponse(t *testing.T, s hostvitals.Snapshot) daemonrpc.Response {
	t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return daemonrpc.Response{Result: data}
}

// sampledSnapshot is a snapshot with swap over its line, the process vitals, two
// processes in the top list and the two vitals not sampled yet.
func sampledSnapshot() hostvitals.Snapshot {
	at := time.Date(2026, 11, 20, 21, 55, 0, 0, time.UTC)
	return hostvitals.Snapshot{
		Enabled: true,
		Sampled: true,
		At:      &at,
		Reading: hostvitals.Reading{SwapOK: true, SwapUsed: 6 << 30, SwapTotal: 8 << 30},
		Thresholds: []hostvitals.Threshold{
			{Name: "swap used", Line: "over 50% of total", State: "over", Detail: "6.0 GiB of 8.0 GiB (75%)"},
			{Name: "memory pressure", Line: "no line", State: "none", Detail: "level 1, 41% free"},
			{Name: "fseventsd RSS", Line: "over 4 GiB", State: "under", Detail: "0.0 GiB"},
			{Name: "process RSS", Line: "over 20 GiB", State: "over", Detail: "job (pid 41) 21.0 GiB"},
		},
		Top: []hostvitals.Proc{
			{PID: 41, PPID: 1, RSSBytes: 21 << 30, ElapsedSeconds: 90061, CPUSeconds: 3.5, Name: "job"},
			{PID: 7, PPID: 1, RSSBytes: 2 << 30, ElapsedSeconds: 59, CPUSeconds: 0.25, Name: "Acme Helper"},
		},
		Gaps: []hostvitals.Gap{
			{Vital: "orphan CPU", Section: "2.1", Reason: "not sampled yet"},
			{Vital: "SSD wear", Section: "2", Reason: "not sampled yet"},
		},
	}
}

func pointAt(t *testing.T, socket string) {
	t.Helper()
	resolveFixture(t, false)
	t.Setenv(config.SocketEnv, socket)
}

// The text view shows the sample time, each threshold with its state and each
// gap line with its design section.
func TestGetVitalsTextShowsSampleThresholdsAndGaps(t *testing.T) {
	socket, _ := vitalsFakeDaemon(t, snapshotResponse(t, sampledSnapshot()))
	pointAt(t, socket)

	var out bytes.Buffer
	if err := getVitals(&out, ""); err != nil {
		t.Fatalf("getVitals: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"2026-11-20T21:55:00Z",
		"swap used", "over 50% of total", "over",
		"memory pressure",
		"fseventsd RSS", "process RSS", "job (pid 41) 21.0 GiB",
		"GAP orphan CPU", "section 2.1",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
}

// -o json prints the daemon's snapshot as a document a script can read.
func TestGetVitalsJSON(t *testing.T) {
	socket, _ := vitalsFakeDaemon(t, snapshotResponse(t, sampledSnapshot()))
	pointAt(t, socket)

	var out bytes.Buffer
	if err := getVitals(&out, "json"); err != nil {
		t.Fatalf("getVitals json: %v", err)
	}
	var got hostvitals.Snapshot
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not a snapshot: %v\n%s", err, out.String())
	}
	if !got.Sampled || len(got.Thresholds) != 4 || len(got.Gaps) != 2 || len(got.Top) != 2 {
		t.Errorf("snapshot = %+v, want the daemon's record", got)
	}
}

// Only json is a document format here; anything else is refused by name and
// the daemon is not asked.
func TestGetVitalsRefusesOtherFormats(t *testing.T) {
	socket, methods := vitalsFakeDaemon(t, snapshotResponse(t, sampledSnapshot()))
	pointAt(t, socket)

	err := getVitals(&bytes.Buffer{}, "yaml")
	if err == nil || !strings.Contains(err.Error(), "json") {
		t.Errorf("err = %v, want a refusal that names json", err)
	}
	if got := methods(); len(got) != 0 {
		t.Errorf("methods = %v, want none for a refused format", got)
	}
}

// A daemon whose cluster left host vitals off is told apart from one that
// has not sampled yet, and each says how to proceed.
func TestGetVitalsOffAndNotYetSampled(t *testing.T) {
	for _, tc := range []struct {
		name string
		snap hostvitals.Snapshot
		want string
	}{
		{"off", hostvitals.Snapshot{}, "host_vitals"},
		{"no sample yet", hostvitals.Snapshot{Enabled: true, Gaps: sampledSnapshot().Gaps}, "no sample yet"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket, _ := vitalsFakeDaemon(t, snapshotResponse(t, tc.snap))
			pointAt(t, socket)
			var out bytes.Buffer
			if err := getVitals(&out, ""); err != nil {
				t.Fatalf("getVitals: %v", err)
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("output lacks %q:\n%s", tc.want, out.String())
			}
		})
	}
}

// A daemon error is passed on, not swallowed into an empty table.
func TestGetVitalsPassesADaemonErrorOn(t *testing.T) {
	socket, _ := vitalsFakeDaemon(t, daemonrpc.Response{Error: "refused for a test"})
	pointAt(t, socket)

	err := getVitals(&bytes.Buffer{}, "")
	if err == nil || !strings.Contains(err.Error(), "refused for a test") {
		t.Errorf("err = %v, want the daemon's error", err)
	}
}

// The verb makes one host.vitals call, and nothing that writes.
func TestGetVitalsMakesOneCall(t *testing.T) {
	socket, methods := vitalsFakeDaemon(t, snapshotResponse(t, sampledSnapshot()))
	pointAt(t, socket)

	if err := getVitals(&bytes.Buffer{}, ""); err != nil {
		t.Fatalf("getVitals: %v", err)
	}
	if got := methods(); len(got) != 1 || got[0] != "host.vitals" {
		t.Errorf("methods = %v, want exactly [host.vitals]", got)
	}
}

// `marvel get vitals` is wired to the verb: the command reaches host.vitals
// and not the generic get.
func TestGetVitalsIsWiredIntoGet(t *testing.T) {
	socket, methods := vitalsFakeDaemon(t, snapshotResponse(t, sampledSnapshot()))
	pointAt(t, socket)

	code, out, errOut := runFanGet(t, "get", "vitals")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if !strings.Contains(out, "swap used") {
		t.Errorf("stdout lacks the swap row:\n%s", out)
	}
	if got := methods(); len(got) != 1 || got[0] != "host.vitals" {
		t.Errorf("methods = %v, want exactly [host.vitals]", got)
	}

	code, out, errOut = runFanGet(t, "get", "vitals", "-o", "json")
	if code != 0 {
		t.Fatalf("-o json: exit %d, stderr %q", code, errOut)
	}
	var snap hostvitals.Snapshot
	if err := json.Unmarshal([]byte(out), &snap); err != nil || !snap.Sampled {
		t.Errorf("-o json output is not the snapshot (err %v):\n%s", err, out)
	}
}

// The top list shows each process's pid, parent, RSS, elapsed time, CPU time
// and command name, with the name only and nothing after it.
func TestGetVitalsShowsTheTopProcesses(t *testing.T) {
	socket, _ := vitalsFakeDaemon(t, snapshotResponse(t, sampledSnapshot()))
	pointAt(t, socket)

	var out bytes.Buffer
	if err := getVitals(&out, ""); err != nil {
		t.Fatalf("getVitals: %v", err)
	}
	text := out.String()
	for _, want := range []string{"top processes by RSS", "PID", "PPID", "RSS", "ELAPSED", "CPU", "NAME", "41", "21.0 GiB", "1d1h1m1s", "3.5s", "job", "Acme Helper", "59s"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "job") > strings.Index(text, "Acme Helper") {
		t.Errorf("the larger process is not listed first:\n%s", text)
	}
}

// The vitals the build now samples are not gap lines in the table.
func TestGetVitalsHasNoGapLineForASampledVital(t *testing.T) {
	socket, _ := vitalsFakeDaemon(t, snapshotResponse(t, sampledSnapshot()))
	pointAt(t, socket)

	var out bytes.Buffer
	if err := getVitals(&out, ""); err != nil {
		t.Fatalf("getVitals: %v", err)
	}
	if !strings.Contains(out.String(), "top processes by RSS") {
		t.Errorf("output lacks the top list that replaces them:\n%s", out.String())
	}
	for _, bad := range []string{"GAP fseventsd RSS", "GAP process RSS"} {
		if strings.Contains(out.String(), bad) {
			t.Errorf("output has %q:\n%s", bad, out.String())
		}
	}
}
