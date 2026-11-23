package config

import (
	"reflect"
	"testing"
)

// With no seat_env.secrets block the cluster maps the bd need to the dolt
// password variable, so a seat holds the password only when its role declares
// seat_secrets: [bd].
func TestAnUnsetSecretsBlockMapsBdByDefault(t *testing.T) {
	want := map[string]string{"bd": "BEADS_DOLT_PASSWORD"}
	plain := seatCluster(t, "name: local\n")
	noSecrets := seatCluster(t, "name: local\nseat_env:\n  path_prepend: [\"/opt/x/bin\"]\n")
	for name, cl := range map[string]*Cluster{
		"no cluster":               nil,
		"no seat_env":              &plain,
		"seat_env without secrets": &noSecrets,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := cl.SeatEnvSpec()
			if err != nil {
				t.Fatalf("SeatEnvSpec: %v", err)
			}
			if !reflect.DeepEqual(got.Secrets, want) {
				t.Errorf("secrets = %v, want the default %v", got.Secrets, want)
			}
		})
	}
}

// An explicit empty secrets block opts out of the default: the cluster keeps
// every seat's environment as it was.
func TestAnEmptySecretsBlockOptsOutOfTheDefault(t *testing.T) {
	cl := seatCluster(t, "name: local\nseat_env:\n  secrets: {}\n")
	got, err := cl.SeatEnvSpec()
	if err != nil {
		t.Fatalf("SeatEnvSpec: %v", err)
	}
	if len(got.Secrets) != 0 {
		t.Errorf("secrets = %v, want none", got.Secrets)
	}
}

// A block that maps its own needs replaces the default and does not add to it.
func TestADeclaredSecretsBlockReplacesTheDefault(t *testing.T) {
	cl := seatCluster(t, "name: local\nseat_env:\n  secrets:\n    bus-reader: MX_BUS_READ\n")
	got, err := cl.SeatEnvSpec()
	if err != nil {
		t.Fatalf("SeatEnvSpec: %v", err)
	}
	if want := map[string]string{"bus-reader": "MX_BUS_READ"}; !reflect.DeepEqual(got.Secrets, want) {
		t.Errorf("secrets = %v, want %v", got.Secrets, want)
	}
}
