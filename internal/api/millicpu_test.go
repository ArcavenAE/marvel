package api

import "testing"

func TestMillicpuFromPercent(t *testing.T) {
	for _, c := range []struct {
		percent float64
		want    int64
	}{
		{0, 0},
		{0.1, 1},
		{12.75, 128},
		{100, 1000},
		{968.1, 9681},
	} {
		if got := MillicpuFromPercent(c.percent); got != c.want {
			t.Errorf("MillicpuFromPercent(%v) = %d, want %d", c.percent, got, c.want)
		}
	}
}
