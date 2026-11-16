package runtime

import "testing"

// marvel run into a team that has the named role goes through the adapter, and
// the session carries the --env values on its own runtime, not on the role's.
// The process environment must hold them too (marvel#1450).
func TestBaseEnvCarriesTheRunEnvOfASessionIntoARole(t *testing.T) {
	t.Parallel()

	ctx := testContext()
	ctx.Role.Runtime.Env = map[string]string{"FROM_ROLE": "role-value"}
	ctx.Session.Runtime.Env = map[string]string{
		"TWIN_CWD": "/work",
		"NATS_URL": "nats://elsewhere:4222",
	}
	env := baseEnv(ctx)

	if got := env["TWIN_CWD"]; got != "/work" {
		t.Fatalf("TWIN_CWD = %q, want %q: --env values reach the process", got, "/work")
	}
	if got := env["FROM_ROLE"]; got != "role-value" {
		t.Errorf("FROM_ROLE = %q, want the role's value kept", got)
	}
	if v, ok := env["NATS_URL"]; ok && v == "nats://elsewhere:4222" {
		t.Errorf("NATS_URL = %q, want the session's value refused as a role's is", v)
	}
}
