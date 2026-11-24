package config

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/arcavenae/marvel/internal/paths"
	"github.com/arcavenae/marvel/internal/shorttmp"
)

// saveAndLoad writes the config through Save, as marvel config add-cluster,
// use-cluster and remove-cluster do, and reads it back through Load.
func saveAndLoad(t *testing.T, cl Cluster) Cluster {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(paths.HomeEnv, filepath.Join(shorttmp.Dir(t), "mh"))
	if err := Save(&Config{Clusters: []Cluster{cl}, CurrentCluster: cl.Name}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Clusters) != 1 {
		t.Fatalf("Load returned %d clusters, want 1", len(cfg.Clusters))
	}
	return cfg.Clusters[0]
}

// secrets: {} keeps every seat inheriting the variable. A save must not turn it
// back into the default, which strips it.
func TestSeatEnvSecretsOptOutSurvivesASave(t *testing.T) {
	cl := saveAndLoad(t, seatCluster(t, "name: local\nseat_env:\n  secrets: {}\n"))
	got, err := cl.SeatEnvSpec()
	if err != nil {
		t.Fatalf("SeatEnvSpec: %v", err)
	}
	if len(got.Secrets) != 0 {
		t.Errorf("after a save the opt-out became %v, want no mapping", got.Secrets)
	}
}

// An absent block, and a block that says nothing about secrets, keep the bd
// default after a save: writing the config must not turn the default into an
// opt-out.
func TestSeatEnvSecretsDefaultSurvivesASave(t *testing.T) {
	want := map[string]string{"bd": "BEADS_DOLT_PASSWORD"}
	for name, y := range map[string]string{
		"no seat_env block":       "name: local\n",
		"a block without secrets": "name: local\nseat_env:\n  unset: [AWS_PROFILE]\n",
		"an empty block":          "name: local\nseat_env: {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			cl := saveAndLoad(t, seatCluster(t, y))
			got, err := cl.SeatEnvSpec()
			if err != nil {
				t.Fatalf("SeatEnvSpec: %v", err)
			}
			if !reflect.DeepEqual(got.Secrets, want) {
				t.Errorf("after a save the secrets are %v, want the default %v", got.Secrets, want)
			}
		})
	}
}

// A declared mapping is written back as it was.
func TestSeatEnvSecretsMappingSurvivesASave(t *testing.T) {
	cl := saveAndLoad(t, seatCluster(t, "name: local\nseat_env:\n  secrets:\n    bd: MX_PW\n"))
	got, err := cl.SeatEnvSpec()
	if err != nil {
		t.Fatalf("SeatEnvSpec: %v", err)
	}
	if want := (map[string]string{"bd": "MX_PW"}); !reflect.DeepEqual(got.Secrets, want) {
		t.Errorf("after a save the secrets are %v, want %v", got.Secrets, want)
	}
}
