package api

import (
	"reflect"
	"strings"
	"testing"
)

// seat_secrets is a per-role key, in TOML and in YAML, carried to the applied
// role. It holds names, never values.
func TestSeatSecretsIsARoleKeyCarriedToTheAppliedRole(t *testing.T) {
	m, err := imageManifest(t, `  seat_secrets = ["bd", "bus-reader"]`)
	if err != nil {
		t.Fatalf("toml: %v", err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	team, err := store.GetTeam("aae/squad")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"bd", "bus-reader"}
	if got := team.Roles[0].SeatSecrets; !reflect.DeepEqual(got, want) {
		t.Errorf("toml seat_secrets = %v, want %v", got, want)
	}
	y := "workspace:\n  name: aae\nteams:\n  - name: squad\n    roles:\n      - name: w\n        replicas: 1\n        seat_secrets: [bd]\n        runtime:\n          command: pi\n"
	ym, err := ParseManifestBytes([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	ys := NewStore()
	if err := ym.Apply(ys); err != nil {
		t.Fatal(err)
	}
	yt, _ := ys.GetTeam("aae/squad")
	if got := yt.Roles[0].SeatSecrets; !reflect.DeepEqual(got, []string{"bd"}) {
		t.Errorf("yaml seat_secrets = %v, want [bd]", got)
	}
}

func TestSeatSecretsAbsentMeansNone(t *testing.T) {
	m, err := imageManifest(t, "")
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore()
	if err := m.Apply(store); err != nil {
		t.Fatal(err)
	}
	team, _ := store.GetTeam("aae/squad")
	if n := len(team.Roles[0].SeatSecrets); n != 0 {
		t.Errorf("an absent seat_secrets gave %d needs", n)
	}
}

// A name that is not a need name is refused when the manifest is read, naming the
// key; a duplicate is refused too. A value pasted where a name belongs is never
// repeated in the error.
func TestSeatSecretsRefusesWhatIsNotANeedName(t *testing.T) {
	for _, tc := range []struct{ name, extra, mention string }{
		{"a space", `  seat_secrets = ["big need"]`, "seat_secrets"},
		{"upper case", `  seat_secrets = ["BD"]`, "seat_secrets"},
		{"empty", `  seat_secrets = [""]`, "seat_secrets"},
		{"a duplicate", `  seat_secrets = ["bd", "bd"]`, "seat_secrets"},
		{"a value", `  seat_secrets = ["s3cr3t value 1234"]`, "seat_secrets"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := imageManifest(t, tc.extra)
			if err == nil {
				err = m.Apply(NewStore())
			}
			if err == nil {
				t.Fatal("the manifest was accepted")
			}
			if !strings.Contains(err.Error(), tc.mention) {
				t.Errorf("error %q does not name %s", err, tc.mention)
			}
			if strings.Contains(err.Error(), "s3cr3t") {
				t.Errorf("the refusal repeats the value: %v", err)
			}
		})
	}
}
