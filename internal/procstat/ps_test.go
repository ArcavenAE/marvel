package procstat

import "testing"

// Captured from `ps -A -o pid=,ppid=,pcpu=,rss=` on macOS 15. Column
// padding varies with the widest value in the table, which is the reason
// the parser splits on fields instead of slicing offsets.
const psFixture = `    1     0   0.2  40688
  295  2842   0.0 159184
18060 13043   1.3   2608
`

func TestParsePS(t *testing.T) {
	got := parsePS(psFixture)
	if len(got) != 3 {
		t.Fatalf("parsePS returned %d entries, want 3", len(got))
	}

	want := []entry{
		{pid: 1, ppid: 0, cpuPercent: 0.2, rssBytes: 40688 * 1024},
		{pid: 295, ppid: 2842, cpuPercent: 0.0, rssBytes: 159184 * 1024},
		{pid: 18060, ppid: 13043, cpuPercent: 1.3, rssBytes: 2608 * 1024},
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], w)
		}
	}
	if got[0].cumulative {
		t.Error("ps entries must not be marked cumulative: pcpu is already a percentage")
	}
}

func TestParsePSSkipsUnparseableLines(t *testing.T) {
	in := "  100    1   0.5  1024\n" +
		"\n" +
		"garbage line\n" +
		"  101\n" + // truncated row, e.g. process exited mid-write
		"  x    1   0.5  1024\n" +
		"  102    1   nope  1024\n" +
		"  103    1   0.5  1024\n"
	got := parsePS(in)
	if len(got) != 2 {
		t.Fatalf("parsePS returned %d entries, want 2 (only pids 100 and 103 are complete)", len(got))
	}
	if got[0].pid != 100 || got[1].pid != 103 {
		t.Errorf("kept pids %d and %d, want 100 and 103", got[0].pid, got[1].pid)
	}
}

func TestParsePSEmpty(t *testing.T) {
	if got := parsePS(""); len(got) != 0 {
		t.Errorf("parsePS(\"\") returned %d entries, want 0", len(got))
	}
}
