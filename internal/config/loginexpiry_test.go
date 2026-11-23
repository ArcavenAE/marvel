package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// login_expiry turns the login expiry service on or off for a cluster, and an
// unset key reads DefaultLoginExpiry.
func TestClusterLoginExpiry(t *testing.T) {
	t.Parallel()
	parse := func(doc string) *Cluster {
		var c Cluster
		if err := yaml.Unmarshal([]byte(doc), &c); err != nil {
			t.Fatalf("unmarshal %q: %v", doc, err)
		}
		return &c
	}
	on, off, unset := parse("name: local\nlogin_expiry: true\n"), parse("name: local\nlogin_expiry: false\n"), parse("name: local\n")
	var nilCluster *Cluster
	if !on.LoginExpiryOn() {
		t.Errorf("login_expiry: true reads off")
	}
	if off.LoginExpiryOn() {
		t.Errorf("login_expiry: false reads on")
	}
	if got := unset.LoginExpiryOn(); got != DefaultLoginExpiry {
		t.Errorf("unset reads %v, want the default %v", got, DefaultLoginExpiry)
	}
	if got := nilCluster.LoginExpiryOn(); got != DefaultLoginExpiry {
		t.Errorf("a nil cluster reads %v, want the default %v", got, DefaultLoginExpiry)
	}
}
