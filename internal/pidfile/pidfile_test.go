package pidfile

import (
	"os"
	"path/filepath"
	"testing"
)

// A pidfile names one process, as a plain decimal number that fits the pid the
// kernel takes: kill(2) and kern.procargs2 read 32 bits, so a wider value wraps
// to another process, to this one, to -1 (every process) or to -5 (a process
// group). Nothing here signals a process; it only reads text.
func TestParse(t *testing.T) {
	for text, want := range map[string]int{
		"4242":       4242,
		"4242\n":     4242,
		"  4242 \n":  4242,
		"1":          1,
		"2147483647": 2147483647, // the largest 32-bit pid
		"007":        7,
		"4242\r\n":   4242,
	} {
		if got, ok := Parse(text); !ok || got != want {
			t.Errorf("Parse(%q) = %d, %v, want %d", text, got, ok, want)
		}
	}
	for _, text := range []string{
		"",
		" \n",
		"0",
		"-5",
		"-1",
		"+4242",
		"12abc",
		"4242xyz",
		"abc12",
		"4242 99",
		"4242\n99",
		"4242,",
		"0x10",
		"4.2",
		"1e3",
		"2147483648", // one past 32 bits
		"4294967297", // wraps to 1
		"4294967295", // wraps to -1
		"4294967291", // wraps to -5
		"9223372036854775807",
		"99999999999999999999",
		"\x004242",
		"4242\x00",
	} {
		if got, ok := Parse(text); ok || got != 0 {
			t.Errorf("Parse(%q) = %d, %v, want no pid", text, got, ok)
		}
	}
}

func TestRead(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if got := Read(write("ok.pid", "4242\n")); got != 4242 {
		t.Errorf("a clean pidfile read %d", got)
	}
	for name, path := range map[string]string{
		"corrupt":     write("bad.pid", "12abc"),
		"wide":        write("wide.pid", "4294967297"),
		"empty":       write("empty.pid", ""),
		"missing":     filepath.Join(dir, "absent.pid"),
		"disabled":    "",
		"a directory": dir,
	} {
		if got := Read(path); got != 0 {
			t.Errorf("%s: read %d, want no pid", name, got)
		}
	}
}
