package hostvitals

import "testing"

const meminfoSample = `MemTotal:       16384000 kB
MemFree:         1000000 kB
SwapCached:            0 kB
SwapTotal:       2097152 kB
SwapFree:        1048576 kB
`

func TestParseMeminfoSwap(t *testing.T) {
	used, total, ok := parseMeminfoSwap(meminfoSample)
	if !ok || total != 2097152<<10 || used != 1048576<<10 {
		t.Errorf("got used=%d total=%d ok=%v", used, total, ok)
	}
}

func TestParseMeminfoSwapRefusesWhatItCannotRead(t *testing.T) {
	for name, in := range map[string]string{
		"empty":         "",
		"no swap lines": "MemTotal: 1 kB\n",
		"free > total":  "SwapTotal: 1 kB\nSwapFree: 2 kB\n",
		"not a number":  "SwapTotal: x kB\nSwapFree: 1 kB\n",
		"no value":      "SwapTotal:\nSwapFree: 1 kB\n",
		"negative":      "SwapTotal: -1 kB\nSwapFree: 0 kB\n",
	} {
		if used, total, ok := parseMeminfoSwap(in); ok {
			t.Errorf("%s: read used=%d total=%d, want no reading", name, used, total)
		}
	}
}

func TestParseMeminfoSwapOnAHostWithNoSwap(t *testing.T) {
	used, total, ok := parseMeminfoSwap("SwapTotal: 0 kB\nSwapFree: 0 kB\n")
	if !ok || used != 0 || total != 0 {
		t.Errorf("got used=%d total=%d ok=%v, want a zero reading", used, total, ok)
	}
}

func TestParsePSISomeAvg10(t *testing.T) {
	const psi = "some avg10=1.25 avg60=0.50 avg300=0.10 total=12345\nfull avg10=0.75 avg60=0.00 avg300=0.00 total=99\n"
	if v, ok := parsePSISomeAvg10(psi); !ok || v != 1.25 {
		t.Errorf("got %v, %v; want 1.25, true (the some line, not the full line)", v, ok)
	}
	for name, in := range map[string]string{
		"empty":     "",
		"full only": "full avg10=0.75 avg60=0 avg300=0 total=1\n",
		"no avg10":  "some avg60=0 total=1\n",
		"garbage":   "some avg10=abc\n",
		"negative":  "some avg10=-1\n",
	} {
		if v, ok := parsePSISomeAvg10(in); ok {
			t.Errorf("%s: read %v, want no reading", name, v)
		}
	}
}
