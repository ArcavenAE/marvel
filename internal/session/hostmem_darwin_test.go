//go:build darwin

package session

import "testing"

// The platform reader answers on a host of this kind, and the answer is a
// percentage.
func TestReadFreeMemoryPercentAnswers(t *testing.T) {
	t.Parallel()
	pct, ok := readFreeMemoryPercent()
	if !ok {
		t.Fatal("readFreeMemoryPercent reported no reading")
	}
	if pct < 0 || pct > 100 {
		t.Errorf("reading = %d, want 0 to 100", pct)
	}
}
