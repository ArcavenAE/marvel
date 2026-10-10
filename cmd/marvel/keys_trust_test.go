package main

import (
	"io"
	"testing"

	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemon"
)

// keys trust asks before it records, and --yes is the scripted form for an
// admin who already compared the fingerprint out of band (marvel#838).
func TestKeysTrustHasAYesFlagThatDefaultsOff(t *testing.T) {
	trust, _, err := keysCmd().Find([]string{"trust"})
	if err != nil || trust == nil || trust.Name() != "trust" {
		t.Fatalf("find keys trust: %v", err)
	}
	f := trust.Flags().Lookup("yes")
	if f == nil {
		t.Fatal("keys trust has no --yes flag")
	}
	if f.DefValue != "false" {
		t.Errorf("--yes default = %q, want false: the command must ask unless told not to", f.DefValue)
	}
}

// Only --yes lets the dial record an unknown key unasked.
func TestKeysTrustDialRecordsUnaskedOnlyWithYes(t *testing.T) {
	if got := keysTrustDialOptions("id", false); got.TrustUnknownHost || got.Identity != "id" {
		t.Errorf("without --yes: %+v, want the prompt path with the identity kept", got)
	}
	if got := keysTrustDialOptions("id", true); !got.TrustUnknownHost {
		t.Errorf("with --yes: %+v, want TrustUnknownHost", got)
	}
}

// The command itself, not only its helper, must dial with the options
// keysTrustDialOptions builds: a restored TrustUnknownHost: true in the
// command would record unasked and pass every test of the helper. No network
// and no daemon: the send is replaced and the options are read.
func TestKeysTrustCommandDialsWithTheOptionsFromTheHelper(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MARVEL_SOCKET", "")
	oldSocket, oldCluster, oldGiven, oldIdentity := socketPath, clusterName, clusterFlagGiven, identityPath
	socketPath, clusterName, clusterFlagGiven, identityPath = "", "", false, ""
	t.Cleanup(func() {
		socketPath, clusterName, clusterFlagGiven, identityPath = oldSocket, oldCluster, oldGiven, oldIdentity
	})
	if err := config.Save(&config.Config{
		Clusters:       []config.Cluster{{Name: "prod", Server: "mrvl://op@127.0.0.1:1", Identity: "/keys/prod"}},
		CurrentCluster: "prod",
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	oldSend := keysTrustSend
	t.Cleanup(func() { keysTrustSend = oldSend })

	for _, tc := range []struct {
		args []string
		yes  bool
	}{
		{[]string{"trust", "prod"}, false},
		{[]string{"trust", "--yes", "prod"}, true},
	} {
		var got daemon.DialOptions
		var addr string
		calls := 0
		keysTrustSend = func(a string, _ daemon.Request, o daemon.DialOptions) (*daemon.Response, error) {
			calls++
			addr, got = a, o
			return &daemon.Response{}, nil
		}
		cmd := keysCmd()
		cmd.SetArgs(tc.args)
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if calls != 1 || addr != "mrvl://op@127.0.0.1:1" {
			t.Fatalf("%v: %d sends to %q, want one to the cluster's address", tc.args, calls, addr)
		}
		if got.TrustUnknownHost != tc.yes || got.Identity != "/keys/prod" {
			t.Errorf("%v: dial options %+v, want TrustUnknownHost=%v and the cluster identity", tc.args, got, tc.yes)
		}
	}
}
