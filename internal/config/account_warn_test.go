package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func warnCluster(t *testing.T, yamlText string) Cluster {
	t.Helper()
	var cl Cluster
	if err := yaml.Unmarshal([]byte(yamlText), &cl); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return cl
}

// The cluster's account warning level: unset is the default 80, 0 is off, 1 to
// 100 is the level, and anything else is an error the caller reports.
func TestClusterAccountWarnLevel(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		want    int
		wantErr bool
	}{
		{"unset is the default", "name: local\nsocket: /tmp/m.sock\n", DefaultAccountWarnPercent, false},
		{"zero is off", "name: local\naccount_warn_percent: 0\n", 0, false},
		{"one", "name: local\naccount_warn_percent: 1\n", 1, false},
		{"seventy", "name: local\naccount_warn_percent: 70\n", 70, false},
		{"one hundred", "name: local\naccount_warn_percent: 100\n", 100, false},
		{"over one hundred", "name: local\naccount_warn_percent: 101\n", 0, true},
		{"negative", "name: local\naccount_warn_percent: -5\n", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cl := warnCluster(t, tc.yaml)
			got, err := cl.AccountWarnLevel()
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Fatalf("level = %d, want %d", got, tc.want)
			}
		})
	}
}

// A nil cluster, which is what a daemon with no entry for its socket has, reads
// as the default.
func TestNilClusterAccountWarnLevel(t *testing.T) {
	var cl *Cluster
	got, err := cl.AccountWarnLevel()
	if err != nil || got != DefaultAccountWarnPercent {
		t.Fatalf("nil cluster level = %d, %v; want %d, nil", got, err, DefaultAccountWarnPercent)
	}
}

// The key survives a save: a level of 0 is written back as 0, not dropped as
// if it were unset.
func TestAccountWarnZeroIsKeptOnSave(t *testing.T) {
	cl := warnCluster(t, "name: local\naccount_warn_percent: 0\n")
	out, err := yaml.Marshal(cl)
	if err != nil {
		t.Fatal(err)
	}
	again := warnCluster(t, string(out))
	got, err := again.AccountWarnLevel()
	if err != nil || got != 0 {
		t.Fatalf("after a round trip the level = %d, %v; want 0 (off)", got, err)
	}
}
