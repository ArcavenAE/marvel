package main

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
)

// badNamesConfig saves a config with one cluster and one refused name.
func badNamesConfig(t *testing.T) {
	t.Helper()
	resolveFixture(t, false)
	cfg := &config.Config{
		Clusters: []config.Cluster{{Name: "testcluster", Socket: "/scratch/cluster.sock"}},
		Display:  config.Display{AccountNames: map[string]string{"ab12cd": "sk-ant-api03-AbCdEf0123456789xyzAbCdEf"}},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	old := accountNames
	t.Cleanup(func() { accountNames = old })
	accountNames = nil
}

// A refused map is not shown: the column loader leaves accountNames empty and
// says so once on stderr, and the note does not repeat the value.
func TestRefusedAccountNamesAreNotShownAndAreNotedOnce(t *testing.T) {
	badNamesConfig(t)
	var err error
	stderr := captureStderr(t, func() { _, _, err = loadSessionColumnsSel("") })
	if err != nil {
		t.Fatalf("loadSessionColumnsSel: %v", err)
	}
	if len(accountNames) != 0 {
		t.Errorf("accountNames = %v after a refused map, want empty", accountNames)
	}
	if n := strings.Count(stderr, "account_names"); n != 1 {
		t.Errorf("stderr names display.account_names %d times, want one note:\n%s", n, stderr)
	}
	if strings.Contains(stderr, "sk-ant") {
		t.Errorf("the note repeats the refused value:\n%s", stderr)
	}
}

// The flag form reads the same config, so the same holds with --columns given.
func TestRefusedAccountNamesAreNotShownWithAColumnsFlag(t *testing.T) {
	badNamesConfig(t)
	stderr := captureStderr(t, func() { _, _, _ = loadSessionColumnsSel("name,account") })
	if len(accountNames) != 0 || strings.Count(stderr, "account_names") != 1 {
		t.Errorf("accountNames = %v, stderr %q; want empty and one note", accountNames, stderr)
	}
}

// A display-only mistake never turns off --cluster: the client config still
// resolves the named cluster.
func TestResolveDaemonRungSurvivesARefusedAccountName(t *testing.T) {
	badNamesConfig(t)
	clusterName = "testcluster"
	addr, _, rung, err := resolveDaemonRung()
	if err != nil {
		t.Fatalf("resolveDaemonRung: %v, want the cluster to resolve despite a bad display name", err)
	}
	if rung != rungCluster || addr != "/scratch/cluster.sock" {
		t.Errorf("rung %q addr %q, want the cluster rung and its socket", rung, addr)
	}
}

// Every command that refuses on any config load error must treat the refusal as
// display-only. get sessions --all-clusters reads the config in collectFanOut.
func TestGetSessionsAllClustersWithARefusedNameExitsZero(t *testing.T) {
	a := startFake(t, fakeAnswers, []api.Session{splitSession("a-one")})
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock})
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Display.AccountNames = map[string]string{"ab12cd": " padded"}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	old := accountNames
	t.Cleanup(func() { accountNames = old })
	var code int
	var out, cmdErr string
	// The note goes to the process's stderr, as the other config warnings do.
	stderr := captureStderr(t, func() {
		code, out, cmdErr = runFanGet(t, "get", "sessions", "--all-clusters", "--columns", "name,account")
	})
	if code != 0 {
		t.Fatalf("exit = %d with a refused display name, want 0; stderr: %s %s", code, cmdErr, stderr)
	}
	if !strings.Contains(out, "a-one") {
		t.Errorf("the table did not render:\n%s", out)
	}
	if n := strings.Count(stderr, "account_names"); n != 1 {
		t.Errorf("stderr names display.account_names %d times, want one note:\n%s", n, stderr)
	}
}

// config list reads the config and refuses on any other error; a refused name
// map must not stop it.
func TestConfigListWithARefusedNameStillLists(t *testing.T) {
	badNamesConfig(t)
	cmd := configCmd()
	cmd.SetArgs([]string{"list"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	if err := cmd.Execute(); err != nil {
		t.Errorf("config list with a refused display name: %v", err)
	}
}
