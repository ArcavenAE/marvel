package config

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func seatCluster(t *testing.T, y string) Cluster {
	t.Helper()
	var cl Cluster
	if err := yaml.Unmarshal([]byte(y), &cl); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return cl
}

// path_prepend: a leading ~ is the home of the user the daemon runs as, and the
// order is kept.
func TestSeatEnvPathPrependExpandsHomeAndKeepsOrder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cl := seatCluster(t, "name: local\nseat_env:\n  path_prepend: [\"~/.local/bin\", /opt/tools/bin, \"~\"]\n")
	got, err := cl.SeatEnvSpec()
	if err != nil {
		t.Fatalf("SeatEnvSpec: %v", err)
	}
	want := []string{filepath.Join(home, ".local/bin"), "/opt/tools/bin", home}
	if !reflect.DeepEqual(got.PathPrepend, want) {
		t.Errorf("path_prepend = %q, want %q", got.PathPrepend, want)
	}
}

func TestSeatEnvUnsetBlockReadsAsNothing(t *testing.T) {
	var nilCluster *Cluster
	for name, cl := range map[string]*Cluster{
		"nil cluster": nilCluster,
		"no block":    ptr(seatCluster(t, "name: local\n")),
	} {
		got, err := cl.SeatEnvSpec()
		if err != nil || len(got.PathPrepend) != 0 || len(got.Unset) != 0 {
			t.Errorf("%s: SeatEnvSpec = %+v, %v; want nothing", name, got, err)
		}
	}
}

func ptr(c Cluster) *Cluster { return &c }

// A name that is not a shell variable name, or a directory that would split
// PATH, is refused when the config loads, naming the entry.
func TestSeatEnvRefusesWhatCannotBeAnEnvironmentName(t *testing.T) {
	for _, tc := range []struct{ name, y, mention string }{
		{"a space", "seat_env:\n  unset: [\"GOOD\", \"bad name\"]\n", "bad name"},
		{"an equals sign", "seat_env:\n  unset: [\"A=B\"]\n", "A=B"},
		{"a leading digit", "seat_env:\n  unset: [\"1X\"]\n", "1X"},
		{"empty", "seat_env:\n  unset: [\"\"]\n", "unset"},
		{"a semicolon", "seat_env:\n  unset: [\"X;rm\"]\n", "X;rm"},
		{"a colon in a directory", "seat_env:\n  path_prepend: [\"/a:/b\"]\n", "/a:/b"},
		{"an empty directory", "seat_env:\n  path_prepend: [\"\"]\n", "path_prepend"},
		{"a relative directory", "seat_env:\n  path_prepend: [\"bin\"]\n", "bin"},
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

func TestSeatEnvAcceptsOrdinaryNames(t *testing.T) {
	cfg := Config{Clusters: []Cluster{seatCluster(t, "name: local\nseat_env:\n  unset: [AWS_PROFILE, _X1, Dolt_Pass]\n  path_prepend: [/opt/a]\n")}}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate refused ordinary names: %v", err)
	}
}
