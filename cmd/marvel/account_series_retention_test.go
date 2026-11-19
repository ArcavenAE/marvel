package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/paths"
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
		{name: "negative env is refused", env: "-24h", wantErr: true},
		{name: "negative flag is refused", flagVal: -time.Hour, flagChanged: true, wantErr: true},
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

// The daemon keeps the series under the home's state directory, and nowhere
// when the home is unknown.
func TestAccountSeriesDirIsUnderTheStateDir(t *testing.T) {
	home := t.TempDir()
	layout := paths.Layout{Home: home}
	want := filepath.Join(home, "state", "account-series")
	if got := accountSeriesDir(layout); got != want {
		t.Fatalf("accountSeriesDir = %q, want %q", got, want)
	}
	if got := accountSeriesDir(paths.Layout{}); got != "" {
		t.Fatalf("accountSeriesDir with no home = %q, want empty", got)
	}
}
