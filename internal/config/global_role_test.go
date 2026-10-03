package config

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// docs/design/global-role-declaration.md sections 3 and 6. A role holds the
// global tier when its manifest says so AND the cluster's config admits its
// name. One resolver answers, so no caller compares a role name.

func role(name, declared string) api.Role {
	return api.Role{Name: name, GlobalRole: declared}
}

// Tests 1 and 3, and the two-key guard's resolver half: the default by name
// holds for an undeclared manifest and widens nothing, a declaration needs
// admission, and "none" opts out.
func TestResolvedGlobalRole(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		role     api.Role
		admitted []string
		want     string
	}{
		{"supervisor undeclared holds it by default", role("supervisor", ""), nil, "supervisor"},
		{"research-supervisor undeclared does not, even if admitted", role("research-supervisor", ""), []string{"research-supervisor"}, ""},
		{"declared and admitted holds it", role("research-supervisor", "supervisor"), []string{"research-supervisor"}, "supervisor"},
		{"declared but not admitted does not", role("research-supervisor", "supervisor"), nil, ""},
		{"declared, admitted under another name only", role("research-supervisor", "supervisor"), []string{"lead"}, ""},
		{"supervisor is admitted without being listed", role("supervisor", "supervisor"), nil, "supervisor"},
		{"supervisor can opt out", role("supervisor", "none"), nil, ""},
		{"a worker declared none stays out", role("worker", "none"), []string{"worker"}, ""},
		{"an unknown word is never returned", role("research-supervisor", "director"), []string{"research-supervisor"}, ""},
		{"a worker with no declaration holds nothing", role("worker", ""), []string{"worker"}, ""},
	}
	for _, tc := range cases {
		if got := ResolvedGlobalRole(tc.role, tc.admitted); got != tc.want {
			t.Errorf("%s: ResolvedGlobalRole(%+v, %v) = %q, want %q", tc.name, tc.role, tc.admitted, got, tc.want)
		}
	}
}

// Test 7: the by-name table is a default for an undeclared manifest, not a
// grant source. Changing it moves only the undeclared roles; a declared role
// ignores it. This replaces the old "the check reads the list" assertion.
func TestDefaultGlobalRoleByNameOnlyMovesUndeclaredRoles(t *testing.T) {
	// No t.Parallel: it swaps a package variable.
	saved := DefaultGlobalRoleByName
	t.Cleanup(func() { DefaultGlobalRoleByName = saved })
	DefaultGlobalRoleByName = map[string]string{"lead": "supervisor"}

	if got := ResolvedGlobalRole(role("lead", ""), []string{"lead"}); got != "supervisor" {
		t.Errorf("an undeclared role in the default table resolves to %q, want supervisor: the resolver does not read the table", got)
	}
	if got := ResolvedGlobalRole(role("supervisor", ""), nil); got != "" {
		t.Errorf("supervisor still resolves to %q after leaving the table: the default is hard-coded", got)
	}
	if got := ResolvedGlobalRole(role("research-supervisor", "supervisor"), []string{"research-supervisor"}); got != "supervisor" {
		t.Errorf("a declared role resolves to %q, want supervisor: it must ignore the table", got)
	}
	if got := ResolvedGlobalRole(role("lead", "none"), []string{"lead"}); got != "" {
		t.Errorf("a role declared none resolves to %q, want none: the table must not override it", got)
	}
}

// global_roles is the operator's admitted list, on the message-bus entry in
// either spelling. Entries are subject tokens, and they resolve through.
func TestBusGlobalRolesValidateAndResolve(t *testing.T) {
	t.Parallel()
	managed := "managed: true\n      listen: 127.0.0.1:4222\n"
	cases := []struct {
		name, block, refuse string
	}{
		{"one name", managed + "      global_roles: [research-supervisor]", ""},
		{"several", managed + "      global_roles: [research-supervisor, lead]", ""},
		{"absent", managed, ""},
		{"supervisor listed is harmless", managed + "      global_roles: [supervisor]", ""},
		{"a dotted name", managed + "      global_roles: [a.b]", "global_roles"},
		{"an empty name", managed + "      global_roles: ['']", "global_roles"},
		{"a name with a space", managed + "      global_roles: ['a b']", "global_roles"},
	}
	for _, tc := range cases {
		cl := parseCluster(t, "clusters:\n  - name: kinu\n    bus:\n      "+tc.block+"\n")
		err := ValidateBus(cl.Name, cl.Bus)
		if tc.refuse == "" && err != nil {
			t.Errorf("%s: refused: %v", tc.name, err)
		}
		if tc.refuse != "" && (err == nil || !errors.Is(err, ErrInvalidBus) || !strings.Contains(err.Error(), tc.refuse)) {
			t.Errorf("%s: err %v, want ErrInvalidBus carrying %q", tc.name, err, tc.refuse)
		}
	}

	cl := parseCluster(t, "clusters:\n  - name: kinu\n    bus:\n      "+managed+"      global_roles: [research-supervisor, lead]\n")
	if got := cl.Bus.Resolve("/state").GlobalRoles; !slices.Equal(got, []string{"research-supervisor", "lead"}) {
		t.Errorf("resolved global_roles = %v, want [research-supervisor lead]", got)
	}

	cl = parseCluster(t, "clusters:\n  - name: kinu\n    services:\n      - name: bus\n        class: message-bus\n        provider: nats-server\n        mode: managed\n        managed: true\n        listen: 127.0.0.1:4222\n        global_roles: [research-supervisor]\n")
	if err := ValidateServices(cl); err != nil {
		t.Fatalf("services spelling with global_roles refused: %v", err)
	}
	svc, err := cl.BusService()
	if err != nil || svc == nil {
		t.Fatalf("BusService = %v, %v", svc, err)
	}
	b, err := svc.BusSpec()
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Resolve("/state").GlobalRoles; !slices.Equal(got, []string{"research-supervisor"}) {
		t.Errorf("services spelling resolved global_roles = %v, want [research-supervisor]", got)
	}
}
