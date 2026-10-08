package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// `marvel daemon` starts the daemon, so a positional argument it does not know
// must be refused, not ignored: a mistyped subcommand such as `status` used to
// start a daemon (marvel#606). The check runs on the parsed command, so the
// test never reaches the code that starts one.
func TestDaemonRefusesAnUnknownSubcommand(t *testing.T) {
	root := &cobra.Command{Use: "marvel"}
	root.AddCommand(daemonCmd())
	daemon, _, err := root.Find([]string{"daemon"})
	if err != nil || daemon.Name() != "daemon" {
		t.Fatalf("find daemon: %v", err)
	}

	for _, arg := range []string{"status", "zzz-bogus-control", "start"} {
		err := daemon.ValidateArgs([]string{arg})
		if err == nil {
			t.Errorf("marvel daemon %s was accepted, so it would start the daemon", arg)
			continue
		}
		if want := `unknown command "` + arg + `" for "marvel daemon"`; !strings.Contains(err.Error(), want) {
			t.Errorf("marvel daemon %s: want %q, got %v", arg, want, err)
		}
	}
	if err := daemon.ValidateArgs(nil); err != nil {
		t.Errorf("marvel daemon with no argument must still start: %v", err)
	}
}

// The real subcommands still resolve to themselves, not to the parent.
func TestDaemonSubcommandsStillResolve(t *testing.T) {
	root := &cobra.Command{Use: "marvel"}
	root.AddCommand(daemonCmd())
	for _, sub := range []string{"logs", "reexec"} {
		c, rest, err := root.Find([]string{"daemon", sub})
		if err != nil || c.Name() != sub || len(rest) != 0 {
			t.Errorf("marvel daemon %s resolved to %v %v (%v)", sub, c.Name(), rest, err)
		}
	}
}
