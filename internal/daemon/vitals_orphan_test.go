package daemon

import (
	"errors"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/hostvitals"
	"github.com/arcavenae/marvel/internal/procargs"
	"github.com/arcavenae/marvel/internal/procstat"
)

// orphanDaemon is a daemon with an on sampler, a store holding one live seat
// whose pane pid is 100, and a marker reader that counts its reads.
func orphanDaemon(t *testing.T, marker func(int) (procargs.Marker, error)) *Daemon {
	t.Helper()
	d := &Daemon{store: api.NewStore(), vitals: hostvitals.New(true, func() hostvitals.Reading {
		return hostvitals.Reading{SwapOK: true, SwapUsed: 1, SwapTotal: 100}
	}, nil)}
	s := &api.Session{
		Name: "seat-0", Workspace: "ws", Team: "t", Role: "r",
		Runtime: api.Runtime{Name: "sleep", Command: "sleep"}, State: api.SessionRunning, PID: 100,
	}
	if err := d.store.CreateSession(s); err != nil {
		t.Fatal(err)
	}
	d.readMarker = marker
	d.wireOrphanSources()
	return d
}

func tableProc(pid, ppid int, name string) procstat.Proc {
	return procstat.Proc{PID: pid, PPID: ppid, RSSBytes: 1 << 20, Name: name, Elapsed: 600e9}
}

// tickVitalsPass runs the twelve passes that end on a vitals pass, handing the
// table to the first, so exactly one reading is taken.
func tickVitalsPass(d *Daemon, table []procstat.Proc) {
	d.vitalsTickWith(table, true)
	for i := 1; i < VitalsEvery; i++ {
		d.vitalsTickWith(nil, false)
	}
}

// The daemon gives the sampler the pane pids of its live seats and a marker
// reader, so a process that was under a seat on one vitals pass and has parent
// pid 1 on the next is marked as an orphan.
func TestVitalsPassMarksTheOrphanOfASeat(t *testing.T) {
	d := orphanDaemon(t, func(int) (procargs.Marker, error) { return procargs.Marker{}, nil })
	tickVitalsPass(d, []procstat.Proc{tableProc(100, 50, "sh"), tableProc(101, 100, "tmux")})
	if got := d.vitals.Snapshot().Orphans; len(got) != 0 {
		t.Fatalf("orphans under the seat = %+v, want none", got)
	}
	tickVitalsPass(d, []procstat.Proc{tableProc(100, 50, "sh"), tableProc(101, 1, "tmux")})
	got := d.vitals.Snapshot().Orphans
	if len(got) != 1 || got[0].PID != 101 || got[0].Reason != hostvitals.ReasonSeatTree {
		t.Errorf("orphans = %+v, want pid 101 marked as a seat tree orphan", got)
	}
}

// The marker reader is the daemon's own and is called for a parentless process
// with no tree ancestry; a read error leaves it unmarked.
func TestVitalsPassReadsTheMarkerOfAParentlessProcess(t *testing.T) {
	var read []int
	d := orphanDaemon(t, func(pid int) (procargs.Marker, error) {
		read = append(read, pid)
		if pid == 302 {
			return procargs.Marker{}, errors.New("operation not permitted")
		}
		return procargs.Marker{Session: "aae/seat-1"}, nil
	})
	tickVitalsPass(d, []procstat.Proc{tableProc(300, 1, "tmux"), tableProc(302, 1, "other"), tableProc(400, 5, "child")})
	got := d.vitals.Snapshot().Orphans
	if len(got) != 1 || got[0].PID != 300 || got[0].Session != "aae/seat-1" || got[0].Reason != hostvitals.ReasonMarker {
		t.Errorf("orphans = %+v, want pid 300 matched by its marker", got)
	}
	for _, pid := range read {
		if pid == 400 {
			t.Errorf("the marker of pid 400, whose parent is not pid 1, was read")
		}
	}
	if len(read) != 2 {
		t.Errorf("marker reads = %v, want pids 300 and 302", read)
	}
}

// A daemon with the sampler off reads no marker; the same daemon with it on
// reads the one parentless process's marker, so the zero is not a daemon that
// never reads.
func TestVitalsOffReadsNoMarker(t *testing.T) {
	for _, tc := range []struct {
		name string
		on   bool
		want int
	}{{"on", true, 1}, {"off", false, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			d := orphanDaemon(t, func(int) (procargs.Marker, error) { reads++; return procargs.Marker{}, nil })
			d.vitals = hostvitals.New(tc.on, func() hostvitals.Reading { return hostvitals.Reading{} }, nil)
			d.wireOrphanSources()
			tickVitalsPass(d, []procstat.Proc{tableProc(300, 1, "tmux")})
			if reads != tc.want {
				t.Errorf("marker reads = %d, want %d", reads, tc.want)
			}
		})
	}
}

// The real daemon is built with the platform's marker reader and wires the
// sources when it loads its sampler.
func TestLoadHostVitalsGivesTheDaemonItsMarkerReader(t *testing.T) {
	d := &Daemon{store: api.NewStore()}
	d.loadHostVitals("/tmp/m-vitals-orphan.sock")
	if d.readMarker == nil {
		t.Error("loadHostVitals left the marker reader nil")
	}
}
