package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestClusterHostVitals: host_vitals turns the host vitals sampler on for a
// cluster, and unset reads as off.
func TestClusterHostVitals(t *testing.T) {
	t.Parallel()
	var on, unset, off Cluster
	if err := yaml.Unmarshal([]byte("name: local\nhost_vitals: true\n"), &on); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := yaml.Unmarshal([]byte("name: local\n"), &unset); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := yaml.Unmarshal([]byte("name: local\nhost_vitals: false\n"), &off); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !on.HostVitals || unset.HostVitals || off.HostVitals {
		t.Fatalf("HostVitals on=%v unset=%v off=%v, want true false false", on.HostVitals, unset.HostVitals, off.HostVitals)
	}
}
