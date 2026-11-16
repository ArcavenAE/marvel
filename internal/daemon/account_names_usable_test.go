package daemon

import (
	"testing"

	"github.com/arcavenae/marvel/internal/config"
)

// A refused display name is a display mistake: the config still loads with no
// error, so the daemon keeps attaching services and keeps the schedule cap and
// the account warn level (review 5914176997, round 2 review 5914369338).
func TestARefusedAccountNameDoesNotMakeTheConfigUnreadable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := &config.Config{
		Clusters: []config.Cluster{{Name: "a", Socket: "/scratch/a.sock"}},
		Display:  config.Display{AccountNames: map[string]string{"ab12cd": "sk-ant-api03-AbCdEf0123456789xyzAbCdEf"}},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load()
	if err != nil || got == nil {
		t.Fatalf("Load = %v, %v; want the config with no error", got, err)
	}
	if !configLoadUsable(err) {
		t.Errorf("configLoadUsable(%v) = false", err)
	}
}
