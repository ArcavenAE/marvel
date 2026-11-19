package main

import (
	"testing"
	"time"
)

func TestResolveAccountSeriesRetention(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		flagVal     time.Duration
		flagChanged bool
		env         string
		want        time.Duration
		wantErr     bool
	}{
		{name: "unset is zero, which the daemon reads as 90 days", want: 0},
		{name: "flag wins", flagVal: 24 * time.Hour, flagChanged: true, env: "48h", want: 24 * time.Hour},
		{name: "env used when the flag is unset", env: "720h", want: 720 * time.Hour},
		{name: "invalid env errors", env: "ninety days", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveAccountSeriesRetention(tc.flagVal, tc.flagChanged, tc.env)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("got %v, %v; want %v, error %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}
