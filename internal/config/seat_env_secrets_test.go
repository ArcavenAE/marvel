package config

import (
	"reflect"
	"strings"
	"testing"
)

// seat_env.secrets maps what a role declares it needs to the variable that
// carries it. Only names ride in config, never a value.
func TestSeatEnvSecretsMapANeedToAVariable(t *testing.T) {
	cl := seatCluster(t, "name: local\nseat_env:\n  secrets:\n    bd: BEADS_DOLT_PASSWORD\n    bus-reader: MX_BUS_READ\n")
	got, err := cl.SeatEnvSpec()
	if err != nil {
		t.Fatalf("SeatEnvSpec: %v", err)
	}
	want := map[string]string{"bd": "BEADS_DOLT_PASSWORD", "bus-reader": "MX_BUS_READ"}
	if !reflect.DeepEqual(got.Secrets, want) {
		t.Errorf("secrets = %v, want %v", got.Secrets, want)
	}
}

func TestSeatEnvSecretsRefuseWhatCannotBeANeedOrAVariable(t *testing.T) {
	for _, tc := range []struct{ name, y, mention string }{
		{"a need with a space", "seat_env:\n  secrets:\n    \"big need\": BEADS_DOLT_PASSWORD\n", "big need"},
		{"an upper case need", "seat_env:\n  secrets:\n    BD: BEADS_DOLT_PASSWORD\n", "BD"},
		{"a variable with a space", "seat_env:\n  secrets:\n    bd: \"BEADS DOLT\"\n", "BEADS DOLT"},
		{"a variable with an equals sign", "seat_env:\n  secrets:\n    bd: \"A=B\"\n", "A=B"},
		{"an empty variable", "seat_env:\n  secrets:\n    bd: \"\"\n", "bd"},
		{"two needs on one variable", "seat_env:\n  secrets:\n    bd: MX_ONE\n    other: MX_ONE\n", "MX_ONE"},
		{"a value where a name belongs", "seat_env:\n  secrets:\n    bd: \"s3cr3t value 1234\"\n", "secrets"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Clusters: []Cluster{seatCluster(t, "name: local\n"+tc.y)}}
			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate accepted it")
			}
			if !strings.Contains(err.Error(), "seat_env") || !strings.Contains(err.Error(), tc.mention) {
				t.Errorf("error %q does not name seat_env and %q", err, tc.mention)
			}
		})
	}
}

// A refusal never repeats a value: the text names the key of the entry.
func TestSeatEnvSecretsRefusalDoesNotRepeatAValue(t *testing.T) {
	cfg := Config{Clusters: []Cluster{seatCluster(t, "name: local\nseat_env:\n  secrets:\n    bd: \"s3cr3t value 1234\"\n")}}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate accepted a value where a variable name belongs")
	}
	if strings.Contains(err.Error(), "s3cr3t") {
		t.Errorf("the refusal repeats the value: %v", err)
	}
}
