package childproof

import (
	"strings"
	"testing"
)

// statLine builds a /proc/<pid>/stat line: field 1 pid, field 2 the command in
// parentheses, then fields 3 to 52. Field 4 is the parent, field 5 is the process group and field 22
// the start time, and every other field is a distinct decimal so a miscount
// reads a different number.
func statLine(comm string, ppid, pgrp, start int) string {
	fields := make([]string, 0, 50)
	for n := 3; n <= 52; n++ {
		switch n {
		case 3:
			fields = append(fields, "S")
		case 4:
			fields = append(fields, itoa(ppid))
		case 5:
			fields = append(fields, itoa(pgrp))
		case 22:
			fields = append(fields, itoa(start))
		default:
			fields = append(fields, itoa(1000+n))
		}
	}
	return "41237 (" + comm + ") " + strings.Join(fields, " ") + "\n"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func TestParseStatReadsFieldsFourFiveAndTwentyTwo(t *testing.T) {
	for name, comm := range map[string]string{
		"plain":                  "nats-server",
		"a space":                "nats server",
		"a closing parenthesis":  "a) b",
		"parentheses and digits": "(1 2) 3 (",
	} {
		ppid, pgrp, ticks, err := parseStat([]byte(statLine(comm, 555, 41237, 9876543)))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if ppid != 555 || pgrp != 41237 || ticks != 9876543 {
			t.Errorf("%s: ppid %d, pgrp %d, start %d; want 555, 41237 and 9876543", name, ppid, pgrp, ticks)
		}
	}
}

func TestParseStatRefusesWhatIsNotAStatLine(t *testing.T) {
	for name, in := range map[string]string{
		"empty":               "",
		"no command":          "41237 S 1 2 3",
		"too few fields":      "41237 (x) S 1 2 3 4 5",
		"a word as the group": strings.Replace(statLine("x", 1, 1, 2), " 1 2 ", " 1 q ", 1),
	} {
		if name == "a word as the group" {
			in = "41237 (x) S 1 word 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23\n"
		}
		if _, _, _, err := parseStat([]byte(in)); err == nil {
			t.Errorf("%s: parseStat accepted %q", name, in)
		}
	}
}
