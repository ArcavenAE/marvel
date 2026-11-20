package daemon

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/hostvitals"
	"github.com/arcavenae/marvel/internal/procstat"
)

// vitalsDaemon builds a daemon with an on sampler and a counting table reader.
func vitalsDaemon(reads *int, table []procstat.Proc, err error) *Daemon {
	d := &Daemon{vitals: hostvitals.New(true, func() hostvitals.Reading {
		return hostvitals.Reading{SwapOK: true, SwapUsed: 1, SwapTotal: 100}
	}, nil)}
	d.readTable = func() ([]procstat.Proc, error) { *reads++; return table, err }
	return d
}

func bigProc() procstat.Proc {
	return procstat.Proc{PID: 41, PPID: 1, RSSBytes: 21 << 30, Elapsed: 90 * time.Second, CPUTime: 3 * time.Second, Name: "job"}
}

func processState(t *testing.T, d *Daemon) string {
	t.Helper()
	for _, th := range d.vitals.Snapshot().Thresholds {
		if th.Name == "process RSS" {
			return th.State
		}
	}
	t.Fatalf("no process RSS threshold in %+v", d.vitals.Snapshot().Thresholds)
	return ""
}

// A vitals pass that is handed the table the metrics pass read makes no read of
// its own: with a seat live, the process vitals cost no exec.
func TestVitalsTickWithATableMakesNoRead(t *testing.T) {
	reads := 0
	d := vitalsDaemon(&reads, nil, nil)
	d.vitalsTickWith([]procstat.Proc{bigProc()}, true)
	if reads != 0 {
		t.Errorf("table reader called %d times with a table in hand, want 0", reads)
	}
	if got := processState(t, d); got != "over" {
		t.Errorf("process RSS = %q, want over from the handed table", got)
	}
	top := d.vitals.Snapshot().Top
	if len(top) != 1 || top[0].Name != "job" || top[0].PID != 41 || top[0].ElapsedSeconds != 90 || top[0].CPUSeconds != 3 {
		t.Errorf("top = %+v, want the handed process with its elapsed and CPU seconds", top)
	}
}

// With no table in hand (no seat has a pid), a vitals pass reads one itself,
// once, and the eleven passes between vitals passes read none.
func TestVitalsTickWithoutATableReadsOncePerVitalsPass(t *testing.T) {
	reads := 0
	d := vitalsDaemon(&reads, []procstat.Proc{bigProc()}, nil)
	for i := 0; i < VitalsEvery; i++ {
		d.vitalsTickWith(nil, false)
	}
	if reads != 1 {
		t.Errorf("%d reads over %d passes, want 1", reads, VitalsEvery)
	}
	d.vitalsTickWith(nil, false)
	if reads != 2 {
		t.Errorf("%d reads on the next vitals pass, want 2", reads)
	}
	if got := processState(t, d); got != "over" {
		t.Errorf("process RSS = %q, want over from the table the tick read", got)
	}
}

// A table read that fails leaves the process vitals a gap and breaks nothing.
func TestVitalsTickTableReadFailureIsAGap(t *testing.T) {
	reads := 0
	d := vitalsDaemon(&reads, nil, errors.New("ps failed"))
	d.vitalsTickWith(nil, false)
	if got := processState(t, d); got != "gap" {
		t.Errorf("process RSS = %q, want gap after a failed read", got)
	}
}

// A daemon with no table reader (a bare test daemon) leaves them a gap too.
func TestVitalsTickWithNoTableReaderIsAGap(t *testing.T) {
	d := &Daemon{vitals: hostvitals.New(true, func() hostvitals.Reading { return hostvitals.Reading{} }, nil)}
	d.vitalsTickWith(nil, false)
	if got := processState(t, d); got != "gap" {
		t.Errorf("process RSS = %q, want gap with no reader", got)
	}
}

// An off sampler reads nothing, the table included; the same daemon with the
// sampler on reads one on its first pass.
func TestVitalsTickOffReadsNoTable(t *testing.T) {
	for _, tc := range []struct {
		name string
		on   bool
		want int
	}{{"on", true, 1}, {"off", false, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			d := vitalsDaemon(&reads, []procstat.Proc{bigProc()}, nil)
			d.vitals = hostvitals.New(tc.on, func() hostvitals.Reading { return hostvitals.Reading{} }, nil)
			for i := 0; i < 30; i++ {
				d.vitalsTickWith(nil, false)
			}
			if tc.on {
				// 30 passes hold the first and the 13th and 25th: three vitals passes.
				tc.want = 3
			}
			if reads != tc.want {
				t.Errorf("table reads = %d, want %d", reads, tc.want)
			}
		})
	}
}

// vitalsDue is true on the first pass and every VitalsEvery-th after it, and
// never while the sampler is off.
func TestVitalsDue(t *testing.T) {
	reads := 0
	d := vitalsDaemon(&reads, nil, nil)
	var due []int
	for i := 0; i < 2*VitalsEvery+1; i++ {
		if d.vitalsDue() {
			due = append(due, i)
		}
		d.vitalsTickWith(nil, false)
	}
	want := []int{0, VitalsEvery, 2 * VitalsEvery}
	if len(due) != len(want) || due[0] != want[0] || due[1] != want[1] || due[2] != want[2] {
		t.Errorf("due on passes %v, want %v", due, want)
	}
	d.vitals = hostvitals.New(false, func() hostvitals.Reading { return hostvitals.Reading{} }, nil)
	d.vitalsPass = 0
	if d.vitalsDue() {
		t.Error("an off sampler is due")
	}
}

// The table reader is wired where the sampler is built, so a started daemon
// has one.
func TestLoadHostVitalsGivesTheDaemonItsTableReader(t *testing.T) {
	d := &Daemon{}
	d.loadHostVitals("/tmp/m-vitals-ps.sock")
	if d.readTable == nil {
		t.Error("loadHostVitals left the table reader nil")
	}
}

// metricsPass is where the table is handed over: with no seat live, one pass
// reads one table through the daemon's reader and the vitals show it, and the
// passes up to the next vitals pass read none.
func TestMetricsPassReadsTheTableOnceOnAVitalsPassWithNoSeat(t *testing.T) {
	skipIfNoTmux(t)
	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	reads := 0
	d.vitals = hostvitals.New(true, func() hostvitals.Reading { return hostvitals.Reading{} }, d.events)
	d.readTable = func() ([]procstat.Proc, error) { reads++; return []procstat.Proc{bigProc()}, nil }

	sampler := procstat.NewSampler()
	for i := 0; i < VitalsEvery; i++ {
		d.metricsPass(sampler, &metricsHold{})
	}
	if reads != 1 {
		t.Errorf("%d table reads over %d passes with no seat, want 1", reads, VitalsEvery)
	}
	if got := processState(t, d); got != "over" {
		t.Errorf("process RSS = %q, want over from the table the pass read", got)
	}
	if !strings.Contains(d.vitals.Snapshot().Top[0].Name, "job") {
		t.Errorf("top = %+v, want the process the reader returned", d.vitals.Snapshot().Top)
	}
}
