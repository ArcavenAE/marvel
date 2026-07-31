package procstat

import (
	"strings"
	"testing"
)

// Shape of a real /proc/<pid>/stat line: ppid 4200, utime 314, stime
// 159, rss 12800 pages. Trailing fields past rsslim are dropped — the
// parser must not require them.
const procStatFixture = "4242 (agent) S 4200 4242 4242 34816 4242 4194304 " +
	"12345 678 9 0 314 159 0 0 20 0 12 0 987654 4823449600 12800 18446744073709551615\n"

func TestParseProcStat(t *testing.T) {
	got, err := parseProcStat(procStatFixture)
	if err != nil {
		t.Fatalf("parseProcStat: %v", err)
	}
	if got.ppid != 4200 {
		t.Errorf("ppid = %d, want 4200", got.ppid)
	}
	if got.cpuTicks != 314+159 {
		t.Errorf("cpuTicks = %d, want %d (utime+stime)", got.cpuTicks, 314+159)
	}
	if got.rssPages != 12800 {
		t.Errorf("rssPages = %d, want 12800", got.rssPages)
	}
}

// The comm field is unquoted and unescaped, so a process named after a
// shell invocation puts both spaces and parentheses inside field 2. This
// is the case that breaks a naive Fields() split, and tmux-spawned
// agents are exactly the processes with such names.
func TestParseProcStatCommWithSpacesAndParens(t *testing.T) {
	in := strings.Replace(procStatFixture, "(agent)", "(sh -c (forestage))", 1)
	got, err := parseProcStat(in)
	if err != nil {
		t.Fatalf("parseProcStat: %v", err)
	}
	if got.ppid != 4200 || got.cpuTicks != 473 || got.rssPages != 12800 {
		t.Errorf("got %+v, want ppid 4200, cpuTicks 473, rssPages 12800", got)
	}
}

func TestParseProcStatRejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"no comm field":  "4242 agent S 4200 4242",
		"truncated line": "4242 (agent) S 4200 4242 4242",
		"empty":          "",
	}
	for name, in := range cases {
		if _, err := parseProcStat(in); err == nil {
			t.Errorf("%s: parseProcStat succeeded, want error", name)
		}
	}
}

const procIOFixture = `rchar: 3891213
wchar: 122880
syscr: 1204
syscw: 88
read_bytes: 2097152
write_bytes: 528384
cancelled_write_bytes: 0
`

func TestParseProcIO(t *testing.T) {
	r, w, err := parseProcIO(procIOFixture)
	if err != nil {
		t.Fatalf("parseProcIO: %v", err)
	}
	if r != 2097152 {
		t.Errorf("readBytes = %d, want 2097152 (read_bytes, not rchar)", r)
	}
	if w != 528384 {
		t.Errorf("writeBytes = %d, want 528384 (write_bytes, not wchar)", w)
	}
}

func TestParseProcIOMissingCounters(t *testing.T) {
	if _, _, err := parseProcIO("rchar: 10\nwchar: 20\n"); err == nil {
		t.Error("parseProcIO succeeded without read_bytes/write_bytes, want error")
	}
}
