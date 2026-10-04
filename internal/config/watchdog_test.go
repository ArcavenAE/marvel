package config

import (
	"testing"
	"time"
)

func TestWatchdogWindow(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"", 0, false},
		{"10m", 10 * time.Minute, false},
		{"90s", 90 * time.Second, false},
		{"0", 0, true},
		{"-5m", 0, true},
		{"soon", 0, true},
	}
	for _, c := range cases {
		got, err := (&Config{Watchdog: Watchdog{Window: c.in}}).WatchdogWindow()
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("%q: got %v, %v", c.in, got, err)
		}
	}
	var nilCfg *Config
	if d, err := nilCfg.WatchdogWindow(); d != 0 || err != nil {
		t.Errorf("nil config: %v, %v", d, err)
	}
}
