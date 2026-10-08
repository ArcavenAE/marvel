package procargs

import (
	"encoding/binary"
	"os"
	"runtime"
	"slices"
	"testing"
)

// The argument list is read exactly: an empty argument and one holding a space
// survive, which a flattened `ps` line loses.
func TestParseCmdlineKeepsEmptyAndSpacedArguments(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"plain", "marvel\x00daemon\x00--mrvl\x00", []string{"marvel", "daemon", "--mrvl"}},
		{"an empty argument", "marvel\x00daemon\x00--mrvl\x00\x00", []string{"marvel", "daemon", "--mrvl", ""}},
		{"an empty argument in the middle", "marvel\x00daemon\x00--state-bolt\x00\x00--reclaim\x00", []string{"marvel", "daemon", "--state-bolt", "", "--reclaim"}},
		{"a space inside an argument", "/opt/My Tools/marvel\x00daemon\x00--socket\x00/run/my dir/m.sock\x00", []string{"/opt/My Tools/marvel", "daemon", "--socket", "/run/my dir/m.sock"}},
	}
	for _, c := range cases {
		got, err := ParseCmdline([]byte(c.in))
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("%s: got %q (%v), want %q", c.name, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "\x00", "marvel\x00daemon"} {
		if got, err := ParseCmdline([]byte(bad)); err == nil {
			t.Errorf("%q: a process with no arguments is unreadable, got %q", bad, got)
		}
	}
}

// procargs2 is argc as a native int32, the executable path, NUL padding, then
// argc NUL-terminated arguments, then the environment, which is not read.
func procargs2(argc int32, exec string, pad int, args []string, env ...string) []byte {
	b := binary.LittleEndian.AppendUint32(nil, uint32(argc))
	b = append(b, exec...)
	b = append(b, make([]byte, 1+pad)...)
	for _, a := range args {
		b = append(append(b, a...), 0)
	}
	for _, e := range env {
		b = append(append(b, e...), 0)
	}
	return b
}

func TestParseProcargs2KeepsEmptyAndSpacedArguments(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want []string
	}{
		{"plain", procargs2(2, "/opt/marvel", 3, []string{"marvel", "daemon"}, "HOME=/h"), []string{"marvel", "daemon"}},
		{"an empty argument", procargs2(4, "/opt/marvel", 0, []string{"marvel", "daemon", "--mrvl", ""}, "HOME=/h"), []string{"marvel", "daemon", "--mrvl", ""}},
		{"a space inside an argument", procargs2(3, "/opt/My Tools/marvel", 5, []string{"/opt/My Tools/marvel", "daemon", "--socket"}, "A=b"), []string{"/opt/My Tools/marvel", "daemon", "--socket"}},
		{"the environment is not read", procargs2(1, "/m", 1, []string{"marvel"}, "X=1", "", "Y=2"), []string{"marvel"}},
	}
	for _, c := range cases {
		got, err := ParseProcargs2(c.in)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("%s: got %q (%v), want %q", c.name, got, err, c.want)
		}
	}
	for name, bad := range map[string][]byte{
		"too short":              {1, 0},
		"argc larger than given": procargs2(5, "/m", 0, []string{"marvel", "daemon"}),
		"no arguments":           procargs2(0, "/m", 0, nil),
		"negative argc":          procargs2(-1, "/m", 0, []string{"marvel"}),
		"an unterminated last":   append(procargs2(2, "/m", 0, []string{"marvel"}), "daemon"...),
	} {
		if got, err := ParseProcargs2(bad); err == nil {
			t.Errorf("%s: want an error, got %q", name, got)
		}
	}
}

// The live reader returns what the process was started with, from the kernel.
func TestReadProcessArgsReadsThisProcessExactly(t *testing.T) {
	got, err := Read(os.Getpid())
	if err != nil {
		t.Fatalf("read own arguments: %v", err)
	}
	if !slices.Equal(got, os.Args) {
		t.Fatalf("read %q, started with %q", got, os.Args)
	}
	if _, err := Read(1 << 30); err == nil {
		t.Error("a pid that does not exist must be an error, not an empty list")
	}
}

// A crafted argc must not size the result: 0x7fffffff would reserve tens of
// gigabytes before the read finds the buffer holds two arguments.
func TestParseProcargs2DoesNotReserveWhatArgcClaims(t *testing.T) {
	in := procargs2(0x7fffffff, "/m", 0, []string{"marvel", "daemon"})
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	got, err := ParseProcargs2(in)
	runtime.ReadMemStats(&after)
	if err == nil {
		t.Fatalf("argc 0x7fffffff over two arguments must be refused, got %q", got)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<20 {
		t.Fatalf("the parse allocated %d bytes for a %d byte buffer", grew, len(in))
	}
}
