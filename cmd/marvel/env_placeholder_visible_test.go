package main

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// A tracked copy of a manifest carries the literal <value> where a secret was
// redacted. Applied before the apply guard existed, it can sit in a stored
// session. The table says so in words and does not print the raw literal, and
// doctor names the seats and the keys.

func placeholderSeat(name string) api.Session {
	s := acctSession(name, "", api.ReadingFresh, "fresh 44% (five_hour)")
	s.Runtime.Env = map[string]string{
		"CLAUDE_CONFIG_DIR": api.EnvPlaceholder,
		"DIRECTOR_CUE":      " " + api.EnvPlaceholder + " ",
		"TWIN_CWD":          api.EnvPlaceholder,
		"OTHER":             "real",
	}
	s.Runtime.AccountLabel = api.EnvPlaceholder
	s.WorkDir = api.EnvPlaceholder
	return s
}

func TestSessionTableShowsAPlaceholderAsAWarningNotTheRawLiteral(t *testing.T) {
	const warn = "unset (placeholder)"
	got := cellsOf(t, "workdir,home,login,account", placeholderSeat("p"))["p"]
	if len(got) != 4 {
		t.Fatalf("cells = %v, want four", got)
	}
	for i, name := range []string{"workdir", "home", "login"} {
		if got[i] != warn {
			t.Errorf("%s cell = %q, want %q", name, got[i], warn)
		}
	}
	for i, c := range got {
		if strings.Contains(c, api.EnvPlaceholder) {
			t.Errorf("cell %d = %q still prints the raw placeholder", i, c)
		}
	}
	// Control: a real value prints as it always did.
	real := acctSession("r", "/acct/a", api.ReadingFresh, "fresh 44% (five_hour)")
	real.Runtime.AccountLabel = "main"
	real.WorkDir = "/work/x"
	r := cellsOf(t, "workdir,home,login", real)["r"]
	if len(r) != 3 || r[0] != "/work/x" || r[1] != "a" || r[2] != "main" {
		t.Errorf("a row with real values = %v, want [/work/x a main]", r)
	}
}

func TestDoctorFlagsASeatWhoseEnvHoldsThePlaceholder(t *testing.T) {
	in := fakeHost(t)
	in.Sessions = func() ([]api.Session, error) {
		ok := api.Session{Name: "good", Pane: "working"}
		ok.Runtime.Env = map[string]string{"CLAUDE_CONFIG_DIR": "/acct/a"}
		bad := placeholderSeat("bad")
		bad.Pane = "working"
		return []api.Session{ok, bad}, nil
	}
	out, failed := doctorRun(in)
	l := lineWith(out, "env-placeholder")
	if !strings.HasPrefix(l, "WARN") {
		t.Fatalf("env-placeholder line = %q, want a WARN line\n%s", l, out)
	}
	for _, want := range []string{"bad", "CLAUDE_CONFIG_DIR", "DIRECTOR_CUE", "TWIN_CWD"} {
		if !strings.Contains(l, want) {
			t.Errorf("line %q lacks %q", l, want)
		}
	}
	if strings.Contains(l, "OTHER") || strings.Contains(l, "good") {
		t.Errorf("line %q names a key or a seat that holds no placeholder", l)
	}
	if n := strings.Count(out, "env-placeholder"); n != 1 {
		t.Errorf("%d env-placeholder lines, want one (for the seat that holds it)\n%s", n, out)
	}
	if failed != 0 {
		t.Errorf("failed = %d, want 0: a placeholder in a stored seat is a warning\n%s", failed, out)
	}
}
