package procargs

import "testing"

func TestParseLsofCwdReadsTheNameLine(t *testing.T) {
	got, err := ParseLsofCwd([]byte("p4242\nfcwd\nn/srv/job dir\n"))
	if err != nil || got != "/srv/job dir" {
		t.Errorf("ParseLsofCwd = %q, %v, want \"/srv/job dir\"", got, err)
	}
}

func TestParseLsofCwdWithoutANameLineIsAnError(t *testing.T) {
	for _, in := range []string{"", "p4242\nfcwd\n", "n"} {
		if got, err := ParseLsofCwd([]byte(in)); err == nil {
			t.Errorf("ParseLsofCwd(%q) = %q, want an error", in, got)
		}
	}
}
