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

// The default is read from the default and not assumed: with the default
// passed in as true, an unset or nil cluster reads true and a cluster that
// names a value keeps it.
func TestClusterLoginExpiryReadsTheDefaultOnlyWhenUnset(t *testing.T) {
	t.Parallel()
	yes, no := true, false
	on, off, unset := &Cluster{LoginExpiry: &yes}, &Cluster{LoginExpiry: &no}, &Cluster{}
	var nilCluster *Cluster
	cases := []struct {
		name string
		c    *Cluster
		def  bool
		want bool
	}{
		{"unset, default on", unset, true, true},
		{"unset, default off", unset, false, false},
		{"nil, default on", nilCluster, true, true},
		{"nil, default off", nilCluster, false, false},
		{"on, default off", on, false, true},
		{"off, default on", off, true, false},
	}
	for _, c := range cases {
		if got := c.c.loginExpiryOr(c.def); got != c.want {
			t.Errorf("%s: reads %v, want %v", c.name, got, c.want)
		}
	}
}

// The service stays off on a cluster that does not set the key. The other tests
// here compare against DefaultLoginExpiry by name, so they follow the constant
// wherever it goes; this one pins the value. Only the keychain proof in
// docs/design/native-checks.md section 2.7 lets it change, and the change to
// true comes with this test.
func TestDefaultLoginExpiryIsOff(t *testing.T) {
	if DefaultLoginExpiry {
		t.Fatal("DefaultLoginExpiry is true: the login expiry service would run on every cluster that does not set the key, before the section 2.7 keychain proof")
	}
	var c Cluster
	if c.LoginExpiryOn() {
		t.Error("a cluster with no login_expiry key reads as on")
	}
}
