package daemon

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// The exposure verbs take a workspace, team and role, and those names are held
// to the same address class everywhere else a name is accepted: lower-case
// letters, digits and dashes, starting with a letter or digit, at most 32 bytes
// (api.ValidateName, aae-orc-bqve7). A name outside the class is refused with
// that rule's own message, before the store is touched.
func TestExposureVerbsRefuseNamesOutsideTheAddressClass(t *testing.T) {
	d := exposureVerbsDaemon(t)

	badNames := map[string]string{
		"upper case":  "Crew",
		"has a space": "the crew",
		"underscore":  "the_crew",
		"too long":    strings.Repeat("a", 33),
	}
	fields := []string{"workspace", "team", "role"}
	verbs := []struct {
		method string
		extra  map[string]any
	}{
		{"exposure.add", nil},
		{"exposure.end", nil},
		{"exposure.void", map[string]any{"reason": "a reason"}},
	}

	for _, v := range verbs {
		for _, f := range fields {
			for label, bad := range badNames {
				extra := map[string]any{f: bad}
				for k, val := range v.extra {
					extra[k] = val
				}
				t.Run(v.method+"/"+f+"/"+label, func(t *testing.T) {
					resp := d.dispatchAs(daemonrpc.Request{Method: v.method, Params: exposureParams(t, extra)}, localCaller())
					if !strings.Contains(resp.Error, "is not addressable") {
						t.Fatalf("%s with %s %q: error = %q, want the address-class refusal", v.method, f, bad, resp.Error)
					}
				})
			}
		}
	}

	if recs, _ := d.store.ExposuresForSlot("xverb", "crew", "writer"); len(recs) != 0 {
		t.Errorf("records for the real slot = %+v, want none: every call above named a bad name", recs)
	}
}

// A well-formed slot still works through every verb.
func TestExposureVerbsStillAcceptAWellFormedSlot(t *testing.T) {
	d := exposureVerbsDaemon(t)
	for _, v := range []struct {
		method string
		extra  map[string]any
	}{
		{"exposure.add", nil},
		{"exposure.end", nil},
		{"exposure.void", map[string]any{"reason": "a reason"}},
	} {
		if resp := d.dispatchAs(daemonrpc.Request{Method: v.method, Params: exposureParams(t, v.extra)}, localCaller()); resp.Error != "" {
			t.Errorf("%s on a well-formed slot: %s", v.method, resp.Error)
		}
	}
}
