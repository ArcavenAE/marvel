package main

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
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

// The real command, Args and RunE both, with a value after a bare -w. The
// table above replaces RunE, so only these see which argument RunE takes as
// the resource type. Each case ends in a refusal that names the resource: with
// "5" taken for it, the message is another one. Under a test there is no
// terminal, so a watch that is allowed ends at "watch mode requires a terminal".
func TestWatchValueReachesRunEAsTheResourceType(t *testing.T) {
	a := startFake(t, fakeAnswers, []api.Session{row("w", "s1")})
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock})
	const across = "watch mode requires a terminal"
	tests := map[string]struct {
		args []string
		want string
	}{
		"value before type, all clusters": {[]string{"get", "-w", "5", "sessions", "--all-clusters"}, across},
		"value after type, all clusters":  {[]string{"get", "sessions", "-w", "5", "--all-clusters"}, across},
		"bare, all clusters":              {[]string{"get", "-w", "sessions", "--all-clusters"}, across},
		"equals, all clusters":            {[]string{"get", "sessions", "-w=5", "--all-clusters"}, across},
		"value before type, teams load":   {[]string{"get", "-w", "5", "teams", "--load"}, "--load and --watch cannot be combined"},
		"value after type, teams load":    {[]string{"get", "teams", "-w", "5", "--load"}, "--load and --watch cannot be combined"},
		"value before type, no terminal":  {[]string{"get", "-w", "5", "sessions"}, "watch mode requires a terminal"},
		"value after type, no terminal":   {[]string{"get", "sessions", "-w", "5"}, "watch mode requires a terminal"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, stderr := runFanGet(t, tc.args...)
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("get %v: stderr = %q, want it to hold %q", tc.args, stderr, tc.want)
			}
		})
	}
}
