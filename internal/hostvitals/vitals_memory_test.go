package hostvitals

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const gibB = uint64(1) << 30

// fp is a fixture process with an RSS in GiB.
func fp(pid, ppid int, name string, rssGiB uint64) Proc {
	return Proc{PID: pid, PPID: ppid, RSSBytes: rssGiB * gibB, ElapsedSeconds: 600, Name: name}
}

// Two seats, one process outside every seat. Seat a holds 1 + 6 GiB and seat b
// holds 1 + 4 GiB; the outside process holds 20 GiB and is not summed.
func seatTable() []Proc {
	return []Proc{
		fp(100, 50, "sh", 1), fp(101, 100, "2.1.322", 6),
		fp(200, 50, "sh", 1), fp(201, 200, "node", 4),
		fp(300, 1, "modeld", 20),
	}
}

func seatSampler(read func() Reading) *Sampler {
	s := New(true, read, nil)
	s.now = clockAt(time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC))
	s.SetOrphanSources(OrphanSources{
		Roots: func() []int { return []int{100, 200} },
		RootSession: func(pid int) string {
			return map[int]string{100: "ws/seat-a", 200: "ws/seat-b"}[pid]
		},
	})
	return s
}

func TestSeatRSSSumsEachSeatTreeOnce(t *testing.T) {
	seats, rss := SeatRSS(seatTable(), []int{100, 200})
	if seats != 2 || rss != 12*gibB {
		t.Errorf("SeatRSS = %d seats, %d bytes, want 2 seats, %d bytes", seats, rss, 12*gibB)
	}
	seats, rss = SeatRSS(seatTable(), []int{100, 100, 999})
	if seats != 1 || rss != 7*gibB {
		t.Errorf("a repeated root and a root missing from the table: %d seats, %d bytes, want 1 seat, %d bytes", seats, rss, 7*gibB)
	}
}

// Item 1: one line that sums the seats' RSS against physical memory.
func TestSeatMemoryVitalSumsSeatsAgainstPhysicalMemory(t *testing.T) {
	s := seatSampler(func() Reading { return Reading{MemTotal: 48 * gibB} })
	s.SampleProcs(seatTable(), true)
	got, ok := find(s.Snapshot().Thresholds, "seat memory")
	if !ok {
		t.Fatalf("thresholds %+v have no \"seat memory\" line", s.Snapshot().Thresholds)
	}
	for _, want := range []string{"2 seats", "12.0 GiB", "48.0 GiB", "25%"} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("seat memory detail %q lacks %q", got.Detail, want)
		}
	}
	if got.State != "none" {
		t.Errorf("seat memory state = %q, want none (the design names no line)", got.State)
	}
}

func TestSeatMemoryVitalWithoutPhysicalMemoryStillSumsSeats(t *testing.T) {
	s := seatSampler(func() Reading { return Reading{} })
	s.SampleProcs(seatTable(), true)
	got, ok := find(s.Snapshot().Thresholds, "seat memory")
	if !ok || !strings.Contains(got.Detail, "12.0 GiB") || strings.Contains(got.Detail, "%") {
		t.Errorf("seat memory = %+v, want the 12.0 GiB sum and no percentage", got)
	}
}

func TestSeatMemoryVitalIsAGapWithoutAProcessTable(t *testing.T) {
	s := seatSampler(func() Reading { return Reading{MemTotal: 48 * gibB} })
	s.SampleProcs(nil, false)
	got, ok := find(s.Snapshot().Thresholds, "seat memory")
	if !ok || got.State != "gap" {
		t.Errorf("seat memory = %+v, want state gap", got)
	}
}

// Item 2: disk free for the volumes of the paths the daemon config names.
func TestDiskFreeVitalReportsEachNamedPath(t *testing.T) {
	s := seatSampler(func() Reading { return Reading{} })
	var asked []string
	s.SetDiskPaths([]string{"/fixture/home/.marvel", "/fixture/work"}, func(p string) (uint64, uint64, error) {
		asked = append(asked, p)
		if p == "/fixture/work" {
			return 0, 0, errors.New("no such volume")
		}
		return 500 * gibB, 40 * gibB, nil
	})
	s.SampleProcs(nil, false)
	got, ok := find(s.Snapshot().Thresholds, "disk free")
	if !ok {
		t.Fatalf("thresholds have no \"disk free\" line")
	}
	for _, want := range []string{"/fixture/home/.marvel", "40.0 GiB free", "92% used", "/fixture/work", "unreadable"} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("disk free detail %q lacks %q", got.Detail, want)
		}
	}
	if len(asked) != 2 {
		t.Errorf("stat called for %v, want both paths once", asked)
	}
}

func TestDiskFreeVitalIsAGapWhenNoPathIsNamed(t *testing.T) {
	s := seatSampler(func() Reading { return Reading{} })
	s.SampleProcs(nil, false)
	if got, ok := find(s.Snapshot().Thresholds, "disk free"); !ok || got.State != "gap" {
		t.Errorf("disk free = %+v, want state gap with no path named", got)
	}
}

// Item 3: a process in a seat's tree carries the seat's key, so a binary named
// for its version is attributable.
func TestTopProcessesNameTheSeatTheyBelongTo(t *testing.T) {
	s := seatSampler(func() Reading { return Reading{} })
	s.SampleProcs(seatTable(), true)
	bySeat := map[int]string{}
	for _, p := range s.Snapshot().Top {
		bySeat[p.PID] = p.Seat
	}
	if bySeat[101] != "ws/seat-a" || bySeat[201] != "ws/seat-b" || bySeat[300] != "" {
		t.Errorf("seat by pid = %v, want 101 in ws/seat-a, 201 in ws/seat-b, 300 in none", bySeat)
	}
}

// Item 4: orphan memory beside orphan CPU.
func TestOrphansCarryTheirRSSAndHaveAMemoryVital(t *testing.T) {
	r := newRig(t)
	r.roots = []int{100}
	r.pass(op(100, 50, "sh", 1, 600), fp(101, 100, "modeld", 3))
	r.pass(op(100, 50, "sh", 1, 660), fp(101, 1, "modeld", 3))
	got := r.orphans()
	if len(got) != 1 || got[0].RSSBytes != 3*gibB {
		t.Fatalf("orphans = %+v, want one orphan with %d RSS bytes", got, 3*gibB)
	}
	v := vital(t, r.s, "orphan memory")
	if !strings.Contains(v.Detail, "3.0 GiB") || v.State != "none" {
		t.Errorf("orphan memory = %+v, want 3.0 GiB and state none", v)
	}
}

func TestOrphanMemoryVitalWithNoOrphans(t *testing.T) {
	r := newRig(t)
	r.pass(op(100, 50, "sh", 1, 600))
	v := vital(t, r.s, "orphan memory")
	if v.State != "none" || !strings.Contains(v.Detail, "no orphans") {
		t.Errorf("orphan memory = %+v, want no orphans", v)
	}
}

// Item 6: swap as used GiB, never as a share of a growing total.
func TestSwapDetailIsUsedGiBWithoutAPercentage(t *testing.T) {
	got := swapThreshold(swapReading(3*gibB, 4*gibB))
	if !strings.Contains(got.Detail, "3.0 GiB used") || strings.Contains(got.Detail, "%") {
		t.Errorf("swap detail = %q, want \"3.0 GiB used\" and no percentage", got.Detail)
	}
}

// A host that reports a total of 0 while it swaps is not a host with no swap.
func TestSwapOnDarwinNeverSaysNoSwapConfigured(t *testing.T) {
	old := hostOS
	hostOS = "darwin"
	defer func() { hostOS = old }()
	got := swapThreshold(swapReading(2*gibB, 0))
	if strings.Contains(got.Detail, "no swap configured") || !strings.Contains(got.Detail, "2.0 GiB used") {
		t.Errorf("swap detail = %q, want 2.0 GiB used and never \"no swap configured\"", got.Detail)
	}
}

func TestSwapInsShowAsADeltaBetweenSamples(t *testing.T) {
	n := uint64(1000)
	s := New(true, func() Reading {
		return Reading{SwapOK: true, SwapUsed: gibB, SwapTotal: 4 * gibB, SwapInsOK: true, SwapIns: n}
	}, nil)
	s.Sample()
	if got, _ := find(s.Snapshot().Thresholds, "swap used"); !strings.Contains(got.Detail, "swap-ins: first sample") {
		t.Errorf("first sample detail = %q, want it to say swap-ins have no earlier sample", got.Detail)
	}
	n = 1600
	s.Sample()
	if got, _ := find(s.Snapshot().Thresholds, "swap used"); !strings.Contains(got.Detail, "600 swap-ins since the last sample") {
		t.Errorf("second sample detail = %q, want 600 swap-ins since the last sample", got.Detail)
	}
}

func TestSwapInsSayPlainlyWhenTheHostHasNoCounter(t *testing.T) {
	got := swapThreshold(swapReading(gibB, 4*gibB))
	if !strings.Contains(got.Detail, "swap-ins not available on this host") {
		t.Errorf("swap detail = %q, want it to say swap-ins are not available", got.Detail)
	}
}
