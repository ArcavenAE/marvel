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

	// Every slot a call names: the real slot with one field swapped for a bad name.
	named := map[[3]string]bool{}
	for _, v := range verbs {
		for _, f := range fields {
			for label, bad := range badNames {
				extra := map[string]any{f: bad}
				slot := map[string]string{"workspace": "xverb", "team": "crew", "role": "writer"}
				slot[f] = bad
				named[[3]string{slot["workspace"], slot["team"], slot["role"]}] = true
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

	// The real slot is never the one a call named, so only the named slots can
	// show a record written before the refusal.
	for slot := range named {
		recs, err := d.store.ExposuresForSlot(slot[0], slot[1], slot[2])
		if err != nil {
			t.Fatalf("ExposuresForSlot(%q): %v", slot, err)
		}
		if len(recs) != 0 {
			t.Errorf("records for slot %q = %+v, want none: the call was refused", slot, recs)
		}
	}
	if len(named) != len(fields)*len(badNames) {
		t.Errorf("checked %d slots, want %d: every field and bad name", len(named), len(fields)*len(badNames))
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
