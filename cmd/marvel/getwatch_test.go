package main

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// watchOf parses args with get's real flag set and returns the interval the
// watch flag resolves to, without running the command.
func watchOf(t *testing.T, args ...string) (time.Duration, error) {
	t.Helper()
	cmd := getCmd()
	cmd.SetArgs(args)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.RunE = func(*cobra.Command, []string) error { return nil }
	if err := cmd.Execute(); err != nil {
		return 0, err
	}
	return watchInterval(cmd.Flags().Lookup("watch").Value.String())
}

func TestWatchTakesAnOptionalInterval(t *testing.T) {
	tests := map[string]struct {
		args []string
		want time.Duration
	}{
		"bare short":        {[]string{"sessions", "-w"}, 2 * time.Second},
		"bare long":         {[]string{"sessions", "--watch"}, 2 * time.Second},
		"short equals":      {[]string{"sessions", "-w=5"}, 5 * time.Second},
		"long equals":       {[]string{"sessions", "--watch=7"}, 7 * time.Second},
		"short then space":  {[]string{"sessions", "-w", "5"}, 5 * time.Second},
		"bare before type":  {[]string{"-w", "sessions"}, 2 * time.Second},
		"value before type": {[]string{"-w", "5", "sessions"}, 5 * time.Second},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := watchOf(t, tc.args...)
			if err != nil {
				t.Fatalf("get %v: %v", tc.args, err)
			}
			if got != tc.want {
				t.Errorf("get %v: interval = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestWatchRefusesABadInterval(t *testing.T) {
	for _, args := range [][]string{
		{"sessions", "-w=0"},
		{"sessions", "-w=abc"},
		{"sessions", "-w", "0"},
	} {
		if _, err := watchOf(t, args...); err == nil || !strings.Contains(err.Error(), "invalid watch interval") {
			t.Errorf("get %v: error = %v, want invalid watch interval", args, err)
		}
	}
}

func TestWatchValueIsNotSwallowedWithoutTheFlag(t *testing.T) {
	// A second positional with no -w stays an error, as before.
	if _, err := watchOf(t, "sessions", "5"); err == nil {
		t.Error("get sessions 5: no error, want the extra argument refused")
	}
}
