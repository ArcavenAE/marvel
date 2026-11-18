package session

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// The opencode example profile (examples/sandbox/opencode-local.sb, rurrg-1) is
// the goose profile plus the director shim's exec. Under it the shim can start
// and nothing more: it cannot reach the bus or its state, so the profile keeps
// "writes only in the seat's trees, network to localhost only".

// The profile keeps the order the rulings need: the real home is denied before
// the trees carved back out of it, the bus ports are denied last, exec admits
// the harness, marvel and the shim and nothing else, writes go to the seat's
// trees and /dev only, and it names no host and no user path.
func TestOpenCodeLocalProfileOrderAndContent(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../examples/sandbox/opencode-local.sb")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	idx := func(s string) int { return strings.Index(text, s) }
	denyHome := idx(`(deny file-read* (subpath (param "REALHOME")))`)
	allowRead := idx(`(allow file-read* (subpath (param "SESSION_DIR")) (subpath (param "WORKDIR")))`)
	allowWrite := idx(`(allow file-write* (subpath (param "SESSION_DIR")) (subpath (param "WORKDIR")) (subpath "/dev"))`)
	allowBins := idx(`(allow file-read* (literal (param "HARNESS_BIN")) (literal (param "MARVEL_BIN")) (literal (param "DIRECTOR_MCP_BIN")))`)
	net := idx(`(allow network-outbound (remote ip "localhost:11434"))`)
	missing := false
	for name, i := range map[string]int{"deny home": denyHome, "read trees": allowRead, "write trees": allowWrite, "binaries": allowBins, "network": net} {
		if i < 0 {
			t.Errorf("the profile lacks its %s rule", name)
			missing = true
		}
	}
	if missing {
		return
	}
	if denyHome > allowRead || denyHome > allowWrite || denyHome > allowBins {
		t.Error("the REALHOME deny must come before every allow it carves out")
	}
	for _, port := range []string{"4222", "4242", "7422", "7442", "3307"} {
		deny := idx(`(deny network-outbound (remote ip "localhost:` + port + `"))`)
		if deny < 0 {
			t.Errorf("the profile does not deny localhost:%s", port)
		} else if deny < net {
			t.Errorf("the localhost:%s deny must come after the network allow", port)
		}
	}
	if !strings.HasPrefix(strings.TrimSpace(text), "(version 1)") || !strings.Contains(text, "(deny default)") {
		t.Error("the profile is not deny by default")
	}

	// Exec: one rule, and it names exactly the three binaries.
	execRules := regexp.MustCompile(`(?m)^\(allow process-exec[^\n]*$`).FindAllString(text, -1)
	if len(execRules) != 1 {
		t.Fatalf("%d process-exec rules, want one: %q", len(execRules), execRules)
	}
	var execParams []string
	for _, m := range regexp.MustCompile(`\(literal \(param "([A-Z_]+)"\)\)`).FindAllStringSubmatch(execRules[0], -1) {
		execParams = append(execParams, m[1])
	}
	sort.Strings(execParams)
	if strings.Join(execParams, ",") != "DIRECTOR_MCP_BIN,HARNESS_BIN,MARVEL_BIN" || strings.Count(execRules[0], "(literal") != 3 || strings.Contains(execRules[0], "subpath") {
		t.Errorf("the exec rule admits %v in %q, want exactly the harness, marvel and the shim", execParams, execRules[0])
	}

	// Writes: one rule, the seat's trees and /dev.
	if n := strings.Count(text, "(allow file-write*"); n != 1 {
		t.Errorf("%d file-write rules, want one", n)
	}

	// No host name and no user path. The shim's limit is said in the file.
	for _, bad := range []string{"/Users/", "/home/", ".com", ".local", ".net"} {
		if strings.Contains(text, bad) {
			t.Errorf("the profile names a host or a user path (%s)", bad)
		}
	}
	if !strings.Contains(text, "cannot reach the bus") {
		t.Error("the header does not say the shim cannot reach the bus or its state under this profile")
	}
}

func shimRig(t *testing.T, profileText string) (*sandboxRig, string) {
	t.Helper()
	r := newSandboxRig(t, api.Role{}, api.PlacementLocalhost)
	writeFile(t, r.profile, profileText, 0o644)
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	shim := filepath.Join(dir, "director-mcp")
	writeFile(t, shim, "#!/bin/sh\nexit 0\n", 0o755)
	return r, shim
}

var sandboxDParam = regexp.MustCompile(` -D '([A-Z_]+)=([^']*)'`)

func wrappedParams(t *testing.T, r *sandboxRig) (names []string, values map[string]string) {
	t.Helper()
	sess := r.session()
	plan := r.mgr.planLaunch(sess)
	if plan.sandbox == nil {
		t.Fatalf("plan = %+v", plan)
	}
	plan.command = "'/m/marvel' 'x'"
	if fail := r.mgr.wrapInSandbox(sess, &plan); fail != nil {
		t.Fatal(fail.msg)
	}
	values = map[string]string{}
	for _, m := range sandboxDParam.FindAllStringSubmatch(plan.command, -1) {
		names = append(names, m[1])
		values[m[1]] = m[2]
	}
	return names, values
}

// A profile that names DIRECTOR_MCP_BIN gets it as a sixth -D parameter, the
// absolute, symlink-free path of the director-mcp the launch finds on PATH.
func TestSandboxProfileNamingTheShimGetsItsPath(t *testing.T) {
	t.Parallel()
	r, shim := shimRig(t, "(version 1)\n(deny default)\n(allow process-exec (literal (param \"DIRECTOR_MCP_BIN\")))\n")
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(shim, link); err != nil {
		t.Fatal(err)
	}
	r.mgr.LookPath = func(name string) (string, error) {
		if name == "director-mcp" {
			return link, nil
		}
		return "", errors.New("not found")
	}
	names, values := wrappedParams(t, r)
	want := "DIRECTOR_MCP_BIN,HARNESS_BIN,MARVEL_BIN,REALHOME,SESSION_DIR,WORKDIR"
	if strings.Join(names, ",") != want {
		t.Fatalf("-D parameters = %v, want %s", names, want)
	}
	resolved, _ := filepath.EvalSymlinks(shim)
	if values["DIRECTOR_MCP_BIN"] != resolved {
		t.Errorf("DIRECTOR_MCP_BIN = %q, want the resolved path %q", values["DIRECTOR_MCP_BIN"], resolved)
	}
}

// A profile that does not name it keeps the five parameters, and the launch
// never looks for the shim, so a host without director-mcp starts such a seat.
func TestSandboxProfileNotNamingTheShimKeepsTheFiveParameters(t *testing.T) {
	t.Parallel()
	r, shim := shimRig(t, "(version 1)\n(deny default)\n")
	looked := 0
	r.mgr.LookPath = func(name string) (string, error) {
		if name == "director-mcp" {
			looked++
			return shim, nil
		}
		return "", errors.New("not found")
	}
	names, _ := wrappedParams(t, r)
	if strings.Join(names, ",") != "HARNESS_BIN,MARVEL_BIN,REALHOME,SESSION_DIR,WORKDIR" {
		t.Errorf("-D parameters = %v, want the five", names)
	}
	if looked != 0 {
		t.Errorf("the launch looked for director-mcp %d times though the profile does not name it", looked)
	}
}

// A profile that names it and a host with no director-mcp is refused at site 1
// with the harness reason, and nothing is started unprofiled.
func TestSandboxProfileNamingTheShimIsRefusedWithoutIt(t *testing.T) {
	t.Parallel()
	r, shim := shimRig(t, "(version 1)\n(deny default)\n(allow process-exec (literal (param \"DIRECTOR_MCP_BIN\")))\n")
	r.mgr.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	sess := r.session()
	if err := r.mgr.checkSandbox(sess); !errors.Is(err, ErrPlacementRefused) {
		t.Fatalf("checkSandbox = %v, want a placement refusal", err)
	}
	if rec := r.mgr.PlacementRefusals(); len(rec) != 1 || rec[0].Reason != RefusalSandboxHarness {
		t.Fatalf("refusals = %+v, want one with reason %q", rec, RefusalSandboxHarness)
	}
	r.mgr.LookPath = func(name string) (string, error) {
		if name == "director-mcp" {
			return shim, nil
		}
		return "", errors.New("not found")
	}
	if err := r.mgr.checkSandbox(sess); err != nil {
		t.Fatalf("after the shim is found: %v", err)
	}
	if rec := r.mgr.PlacementRefusals(); len(rec) != 0 {
		t.Errorf("refusal stands after the fix: %+v", rec)
	}
}
