//go:build linux

package suspend

import "testing"

func TestParseStopped(t *testing.T) {
	for _, c := range []struct {
		stat string
		want bool
	}{
		{"4242 (sleep) T 4200 4242 4242 0 -1 4194560", true},
		{"4242 (sleep) S 4200 4242 4242 0 -1 4194560", false},
		{"4242 (sleep) t 4200 4242 4242 0 -1 4194560", false},
		{"4242 (odd) T name) S 4200 4242", false},
		{"4242 (odd) S name) T 4200 4242", true},
	} {
		got, err := parseStopped(c.stat)
		if err != nil || got != c.want {
			t.Errorf("parseStopped(%q) = %v, %v; want %v", c.stat, got, err, c.want)
		}
	}
	if _, err := parseStopped("4242 no command"); err == nil {
		t.Error("a stat with no command field parsed")
	}
}
