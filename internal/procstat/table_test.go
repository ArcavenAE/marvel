package procstat

import (
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ps prints etime as [[dd-]hh:]mm:ss.
func TestParseElapsed(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"00:05", 5 * time.Second, true},
		{"16:19", 16*time.Minute + 19*time.Second, true},
		{"16:19:11", 16*time.Hour + 19*time.Minute + 11*time.Second, true},
		{"07-05:13:12", 7*24*time.Hour + 5*time.Hour + 13*time.Minute + 12*time.Second, true},
		{"  01:02:03", time.Hour + 2*time.Minute + 3*time.Second, true},
		{"", 0, false},
		{"x:y", 0, false},
		{"1-", 0, false},
	} {
		got, ok := parseElapsed(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseElapsed(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// ps prints time as [[h:]m:]s with a fractional seconds part: macOS gives
// minutes and seconds with centiseconds, and the minutes can pass 999.
func TestParseCPUTime(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"0:00.12", 120 * time.Millisecond, true},
		{"424:10.93", 424*time.Minute + 10*time.Second + 930*time.Millisecond, true},
		{"5360:40.47", 5360*time.Minute + 40*time.Second + 470*time.Millisecond, true},
		{"1:02:03.50", time.Hour + 2*time.Minute + 3*time.Second + 500*time.Millisecond, true},
		{"12.5", 12*time.Second + 500*time.Millisecond, true},
		{"", 0, false},
		{"a:b", 0, false},
	} {
		got, ok := parseCPUTime(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseCPUTime(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// The name is the last path element: a path with spaces keeps them, a bare
// name stays as it is, and nothing after the executable can appear.
func TestCommandName(t *testing.T) {
	for in, want := range map[string]string{
		"/System/Library/Frameworks/CoreServices.framework/Support/fseventsd":                   "fseventsd",
		"/Applications/Acme.app/Contents/Frameworks/Acme Helper.app/Contents/MacOS/Acme Helper": "Acme Helper",
		"claude": "claude",
		"-zsh":   "-zsh",
		"":       "",
	} {
		if got := commandName(in); got != want {
			t.Errorf("commandName(%q) = %q, want %q", in, got, want)
		}
	}
}

const wideFixture = `  579     1   0.0  18432 09-16:17:15 113:04.85 /System/Library/Frameworks/CoreServices.framework/Support/fseventsd
28579 23503  11.6 1548416 08-04:01:09 386:53.40 claude
85130     1   0.0 9093248 07-05:13:12 424:10.93 /Applications/Acme.app/Contents/Frameworks/Acme Helper.app/Contents/MacOS/Acme Helper
not a row
  777   579   0.1   2048    00:05   0:00.12 sleep
`

// The wide parse keeps the four columns the rollup uses and adds the three the
// vitals read wants, and a row that does not parse costs only itself.
func TestParsePSWide(t *testing.T) {
	entries, procs := parsePSWide(wideFixture)
	if len(entries) != 4 || len(procs) != 4 {
		t.Fatalf("got %d entries and %d procs, want 4 and 4", len(entries), len(procs))
	}
	wantE := entry{pid: 28579, ppid: 23503, cpuPercent: 11.6, rssBytes: 1548416 * 1024}
	if entries[1] != wantE {
		t.Errorf("entry = %+v, want %+v", entries[1], wantE)
	}
	want := Proc{
		PID: 85130, PPID: 1, RSSBytes: 9093248 * 1024,
		Elapsed: 7*24*time.Hour + 5*time.Hour + 13*time.Minute + 12*time.Second,
		CPUTime: 424*time.Minute + 10*time.Second + 930*time.Millisecond,
		Name:    "Acme Helper",
	}
	if !reflect.DeepEqual(procs[2], want) {
		t.Errorf("proc = %+v, want %+v", procs[2], want)
	}
	if procs[0].Name != "fseventsd" || procs[3].Name != "sleep" {
		t.Errorf("names = %q and %q, want fseventsd and sleep", procs[0].Name, procs[3].Name)
	}
}

// A Sample reads the narrow table, as it always has, unless the caller asked
// for the wide one; a wide Sample keeps the table for one hand-over and
// reads once, not twice.
func TestSamplerKeepsTheWideTableOnlyWhenAsked(t *testing.T) {
	narrow, wide := 0, 0
	table := []Proc{{PID: 9, PPID: 1, RSSBytes: 5 << 30, Name: "big"}}
	oldN, oldW := readNarrow, readWide
	t.Cleanup(func() { readNarrow, readWide = oldN, oldW })
	readNarrow = func() ([]entry, error) { narrow++; return []entry{{pid: 7, ppid: 1}}, nil }
	readWide = func() ([]entry, []Proc, error) { wide++; return []entry{{pid: 7, ppid: 1}}, table, nil }

	s := NewSampler()
	if _, err := s.Sample([]int{7}); err != nil {
		t.Fatal(err)
	}
	if narrow != 1 || wide != 0 {
		t.Errorf("plain sample: %d narrow and %d wide reads, want 1 and 0", narrow, wide)
	}
	if _, ok := s.Table(); ok {
		t.Error("a plain sample kept a table")
	}

	s.WantTable()
	if _, err := s.Sample([]int{7}); err != nil {
		t.Fatal(err)
	}
	if narrow != 1 || wide != 1 {
		t.Errorf("wide sample: %d narrow and %d wide reads in all, want 1 and 1", narrow, wide)
	}
	got, ok := s.Table()
	if !ok || !reflect.DeepEqual(got, table) {
		t.Errorf("Table() = %v, %v; want the wide table, true", got, ok)
	}
	if _, ok := s.Table(); ok {
		t.Error("the table was handed over twice")
	}

	if _, err := s.Sample([]int{7}); err != nil {
		t.Fatal(err)
	}
	if narrow != 2 || wide != 1 {
		t.Errorf("next sample: %d narrow and %d wide reads in all, want 2 and 1 (the request is spent)", narrow, wide)
	}
}

// With no seat pid a Sample reads nothing, wide or not, and keeps no table:
// the vitals read then runs its own, which is the design's one ps a minute.
// The request is spent by that Sample, so a later Sample with a seat does not
// read wide on a pass nobody asked for.
func TestSamplerWithNoRootsKeepsNoTable(t *testing.T) {
	narrow, wide := 0, 0
	oldN, oldW := readNarrow, readWide
	t.Cleanup(func() { readNarrow, readWide = oldN, oldW })
	readNarrow = func() ([]entry, error) { narrow++; return []entry{{pid: 7, ppid: 1}}, nil }
	readWide = func() ([]entry, []Proc, error) {
		wide++
		return []entry{{pid: 7, ppid: 1}}, []Proc{{PID: 7}}, nil
	}

	s := NewSampler()
	s.WantTable()
	if _, err := s.Sample([]int{7}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Table(); !ok || wide != 1 {
		t.Fatalf("a wide sample with a root kept no table (ok %v, %d wide reads)", ok, wide)
	}

	s.WantTable()
	if _, err := s.Sample(nil); err != nil {
		t.Fatal(err)
	}
	if narrow != 0 || wide != 1 {
		t.Errorf("a sample with no roots read the table: %d narrow and %d wide reads in all, want 0 and 1", narrow, wide)
	}
	if _, ok := s.Table(); ok {
		t.Error("a sample with no roots kept a table")
	}

	if _, err := s.Sample([]int{7}); err != nil {
		t.Fatal(err)
	}
	if narrow != 1 || wide != 1 {
		t.Errorf("the sample after it: %d narrow and %d wide reads in all, want 1 and 1 (the request was spent)", narrow, wide)
	}
}

// The Linux fields come from /proc/<pid>/stat: the name between the first '('
// and the last ')', which can hold both, and the start time in ticks.
func TestParseProcStatWide(t *testing.T) {
	const line = "4242 (Acme (worker) 1) S 1 4242 4242 0 -1 4194560 100 0 0 0 7 3 0 0 20 0 1 0 987654 10485760 2560 18446744073709551615"
	name, start, err := parseProcStatWide(line)
	if err != nil || name != "Acme (worker) 1" || start != 987654 {
		t.Errorf("parseProcStatWide = %q, %d, %v; want %q, 987654, nil", name, start, err, "Acme (worker) 1")
	}
	if _, _, err := parseProcStatWide("1 (x) S 1"); err == nil {
		t.Error("a short stat line did not fail")
	}
	if _, _, err := parseProcStatWide("no parens here"); err == nil {
		t.Error("a line with no comm did not fail")
	}
}

func TestParseUptime(t *testing.T) {
	if got, err := parseUptime("12345.67 98765.43\n"); err != nil || got != 12345.67 {
		t.Errorf("parseUptime = %v, %v; want 12345.67", got, err)
	}
	if _, err := parseUptime(""); err == nil {
		t.Error("an empty uptime did not fail")
	}
}

// ReadTable reads the real process table of the host the test runs on. Where
// the table cannot be read (a sandbox that refuses the setuid ps), it skips.
// Names are command names only: no path separator, nothing after the
// executable.
func TestReadTableOnThisHost(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("no process table reader on " + runtime.GOOS)
	}
	procs, err := ReadTable()
	if err != nil {
		t.Skipf("the process table cannot be read here: %v", err)
	}
	var self *Proc
	for i := range procs {
		p := &procs[i]
		if strings.Contains(p.Name, "/") {
			t.Errorf("pid %d: name %q holds a path separator", p.PID, p.Name)
		}
		if p.Elapsed < 0 || p.CPUTime < 0 {
			t.Errorf("pid %d: negative elapsed %v or CPU time %v", p.PID, p.Elapsed, p.CPUTime)
		}
		if p.PID == os.Getpid() {
			self = p
		}
	}
	if self == nil {
		t.Fatalf("this test process (pid %d) is not in a table of %d processes", os.Getpid(), len(procs))
	}
	if self.RSSBytes <= 0 || self.Name == "" || self.PPID <= 0 {
		t.Errorf("this process = %+v, want an RSS, a name and a parent", *self)
	}
}
