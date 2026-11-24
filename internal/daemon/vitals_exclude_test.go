package daemon

import (
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/arcavenae/marvel/internal/config"
)

// The orphan exclude list is the cluster's host_vitals_orphan_exclude, and a
// cluster that sets none, or no cluster at all, excludes nothing.
func TestHostVitalsOrphanExcludeComesFromTheClusterConfig(t *testing.T) {
	var set, unset config.Cluster
	doc := "name: local\nhost_vitals_orphan_exclude:\n  - /fixture/Apps/Vm.app\n  - /fixture/bin/modeld\n"
	if err := yaml.Unmarshal([]byte(doc), &set); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal([]byte("name: local\n"), &unset); err != nil {
		t.Fatal(err)
	}
	if got, want := hostVitalsOrphanExclude(&set), []string{"/fixture/Apps/Vm.app", "/fixture/bin/modeld"}; !reflect.DeepEqual(got, want) {
		t.Errorf("exclude = %v, want %v", got, want)
	}
	if got := hostVitalsOrphanExclude(&unset); len(got) != 0 {
		t.Errorf("unset exclude = %v, want empty", got)
	}
	if got := hostVitalsOrphanExclude(nil); len(got) != 0 {
		t.Errorf("no cluster, exclude = %v, want empty", got)
	}
}
