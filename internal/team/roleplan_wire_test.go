package team

import (
	"reflect"
	"testing"
)

// RolePlan crosses the wire in the daemon's plan result, and the CLI decodes it
// as a []RolePlan. Each field carries a json tag equal to its Go name, which is
// the name the wire has always used, so a rename of the field cannot change the
// wire without changing the tag, and the golden fixture of the plan result
// says so.
func TestRolePlanFieldsCarryTheirWireNameAsATag(t *testing.T) {
	t.Parallel()
	rt := reflect.TypeOf(RolePlan{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		if got := f.Tag.Get("json"); got != f.Name {
			t.Errorf("RolePlan.%s has json tag %q, want %q", f.Name, got, f.Name)
		}
	}
}
