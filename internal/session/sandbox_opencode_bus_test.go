package session

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The opencode bus profile (examples/sandbox/opencode-local-bus.sb, rurrg-1
// part B) is opencode-local.sb plus three widenings the operator ruled: the bus
// port on localhost, a read of the seat's bus password file, and the director
// shim's state directory inside the seat's own tree. marvel fills BUS_PORT and
// BUS_PASS_FILE only for a profile that names them, and sets DIRECTOR_STATE only
// for a profile that names the shim. Tests use scratch paths only.

func TestOpenCodeLocalBusProfileOrderAndContent(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../examples/sandbox/opencode-local-bus.sb")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	idx := func(s string) int { return strings.Index(text, s) }
	denyHome := idx(`(deny file-read* (subpath (param "REALHOME")))`)
	passRead := idx(`(allow file-read* (literal (param "BUS_PASS_FILE")))`)
	busAllow := idx(`(allow network-outbound (remote ip (string-append "localhost:" (param "BUS_PORT"))))`)
	model := idx(`(allow network-outbound (remote ip "localhost:11434"))`)
	for name, i := range map[string]int{"deny home": denyHome, "pass file read": passRead, "bus port allow": busAllow, "model port": model} {
		if i < 0 {
			t.Errorf("the profile lacks its %s rule", name)
		}
	}
	if denyHome < 0 || passRead < 0 || busAllow < 0 || model < 0 {
		return
	}
	if denyHome > passRead {
		t.Error("the REALHOME deny must come before the pass file read it carves out")
	}
	for _, port := range []string{"4242", "7422", "7442", "3307"} {
		deny := idx(`(deny network-outbound (remote ip "localhost:` + port + `"))`)
		if deny < 0 {
			t.Errorf("the profile does not deny localhost:%s", port)
		} else if deny < busAllow {
			t.Errorf("the localhost:%s deny must come after the bus port allow", port)
		}
	}
	if strings.Contains(text, `"localhost:4222"`) {
		t.Error("a literal localhost:4222 rule would cancel or pre-empt the bus port the launch names")
	}

	// Exec: one rule, exactly the three binaries.
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
		t.Errorf("the exec rule admits %v, want exactly the harness, marvel and the shim", execParams)
	}
	// Writes: one rule, the seat's trees and /dev; the state directory is inside them.
	writes := regexp.MustCompile(`(?m)^\(allow file-write\*[^\n]*$`).FindAllString(text, -1)
	if len(writes) != 1 || writes[0] != `(allow file-write* (subpath (param "SESSION_DIR")) (subpath (param "WORKDIR")) (subpath "/dev"))` {
		t.Errorf("write rules = %q, want the one for the seat's trees and /dev", writes)
	}
	// Network: the model server and the bus port, and nothing else allowed.
	if n := strings.Count(text, "(allow network-outbound"); n != 2 {
		t.Errorf("%d network-outbound allows, want two (the model server and the bus port)", n)
	}
	for _, bad := range []string{"/Users/", "/home/", ".com", ".local", ".net"} {
		if strings.Contains(text, bad) {
			t.Errorf("the profile names a host or a user path (%s)", bad)
		}
	}
	// The header names each widening and what a process inside can now do.
	header := text
	if i := strings.Index(text, "(deny default)"); i > 0 {
		header = text[:i]
	}
	for _, want := range []string{
		"BUS_PORT", "BUS_PASS_FILE", "DIRECTOR_STATE",
		"can read the team bus password", "reach the bus on that port",
	} {
		if !strings.Contains(header, want) {
			t.Errorf("the header does not say %q", want)
		}
	}
}

// busRig is a sandbox rig with a private token directory, a bus password file in
// it, a director-mcp the lookup finds, and a launch environment that names the
// bus. Every path is scratch.
type busRig struct {
	*sandboxRig
	passFile string
	env      map[string]string
}

func newBusRig(t *testing.T, profileText string) *busRig {
	t.Helper()
	r, shim := shimRig(t, profileText)
	tokens, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.Chmod(tokens, 0o700); err != nil {
		t.Fatal(err)
	}
	r.mgr.TokenDir = tokens
	pass := filepath.Join(tokens, "seat.pass")
	writeFile(t, pass, "scratch-not-a-password\n", 0o600)
	r.mgr.LookPath = func(name string) (string, error) {
		if name == "director-mcp" {
			return shim, nil
		}
		return "", errors.New("not found")
	}
	return &busRig{sandboxRig: r, passFile: pass, env: map[string]string{
		"NATS_URL":                "nats://127.0.0.1:4333",
		"DIRECTOR_NATS_USER":      "squad",
		"DIRECTOR_NATS_PASS_FILE": pass,
	}}
}

const busProfile = "(version 1)\n(deny default)\n" +
	"(allow process-exec (literal (param \"DIRECTOR_MCP_BIN\")))\n" +
	"(allow file-read* (literal (param \"BUS_PASS_FILE\")))\n" +
	"(allow network-outbound (remote ip (string-append \"localhost:\" (param \"BUS_PORT\"))))\n"

// wrapBus plans and wraps the seat with the rig's environment, and returns the
// -D parameters, the environment after the wrap, and the wrap's failure.
func (r *busRig) wrapBus(t *testing.T) (names []string, values map[string]string, env map[string]string, fail *sandboxFailure) {
	t.Helper()
	sess := r.session()
	plan := r.mgr.planLaunch(sess)
	if plan.sandbox == nil {
		t.Fatalf("plan = %+v", plan)
	}
	plan.command = "'/m/marvel' 'x'"
	plan.env = map[string]string{}
	for k, v := range r.env {
		plan.env[k] = v
	}
	fail = r.mgr.wrapInSandbox(sess, &plan)
	values = map[string]string{}
	for _, m := range sandboxDParam.FindAllStringSubmatch(plan.command, -1) {
		names = append(names, m[1])
		values[m[1]] = m[2]
	}
	return names, values, plan.env, fail
}

// A profile that names the bus parameters and the shim gets them as -D
// parameters and the shim's state directory, inside the seat's own tree.
func TestSandboxProfileNamingTheBusGetsItsParametersAndState(t *testing.T) {
	t.Parallel()
	r := newBusRig(t, busProfile)
	names, values, env, fail := r.wrapBus(t)
	if fail != nil {
		t.Fatal(fail.msg)
	}
	want := "BUS_PASS_FILE,BUS_PORT,DIRECTOR_MCP_BIN,HARNESS_BIN,MARVEL_BIN,REALHOME,SESSION_DIR,WORKDIR"
	if strings.Join(names, ",") != want {
		t.Fatalf("-D parameters = %v, want %s", names, want)
	}
	if values["BUS_PORT"] != "4333" || values["BUS_PASS_FILE"] != r.passFile {
		t.Errorf("BUS_PORT = %q, BUS_PASS_FILE = %q, want 4333 and %q", values["BUS_PORT"], values["BUS_PASS_FILE"], r.passFile)
	}
	state := filepath.Join(r.home, "director-state")
	if env["DIRECTOR_STATE"] != state {
		t.Errorf("DIRECTOR_STATE = %q, want %q", env["DIRECTOR_STATE"], state)
	}
	if info, err := os.Stat(state); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Errorf("state directory %s: %v, %v, want a 0700 directory", state, info, err)
	}
}

// A role's own DIRECTOR_STATE does not survive a profile that names the shim.
func TestSandboxProfileNamingTheShimOverwritesARoleDirectorState(t *testing.T) {
	t.Parallel()
	r := newBusRig(t, busProfile)
	r.env["DIRECTOR_STATE"] = "/somewhere/else"
	_, _, env, fail := r.wrapBus(t)
	if fail != nil {
		t.Fatal(fail.msg)
	}
	if want := filepath.Join(r.home, "director-state"); env["DIRECTOR_STATE"] != want {
		t.Errorf("DIRECTOR_STATE = %q, want %q", env["DIRECTOR_STATE"], want)
	}
}

// Control: a profile that names none of them keeps the five parameters, leaves
// the environment alone, makes no state directory and never looks at the bus.
func TestSandboxProfileNamingNoBusKeepsTheFiveParametersAndEnv(t *testing.T) {
	t.Parallel()
	r := newBusRig(t, "(version 1)\n(deny default)\n")
	r.env["DIRECTOR_STATE"] = "/somewhere/else"
	r.env["NATS_URL"] = "nats://203.0.113.9:4222" // not loopback: would be refused if it were read
	names, _, env, fail := r.wrapBus(t)
	if fail != nil {
		t.Fatal(fail.msg)
	}
	if strings.Join(names, ",") != "HARNESS_BIN,MARVEL_BIN,REALHOME,SESSION_DIR,WORKDIR" {
		t.Errorf("-D parameters = %v, want the five", names)
	}
	if env["DIRECTOR_STATE"] != "/somewhere/else" {
		t.Errorf("DIRECTOR_STATE = %q, want the role's value left alone", env["DIRECTOR_STATE"])
	}
	if _, err := os.Stat(filepath.Join(r.home, "director-state")); err == nil {
		t.Error("a state directory was made for a profile that does not name the shim")
	}
}

// The bus port is read from NATS_URL, and the launch is refused unless it names
// a loopback host and a port.
func TestSandboxBusPortIsReadFromNatsURLAndRefusedWhenUnsafe(t *testing.T) {
	t.Parallel()
	for url, want := range map[string]string{
		"nats://127.0.0.1:4333": "4333",
		"nats://localhost:4222": "4222",
		"nats://[::1]:7000":     "7000",
		"tls://127.0.0.1:65535": "65535",
	} {
		t.Run("accepts "+url, func(t *testing.T) {
			r := newBusRig(t, busProfile)
			r.env["NATS_URL"] = url
			_, values, _, fail := r.wrapBus(t)
			if fail != nil {
				t.Fatalf("refused: %s", fail.msg)
			}
			if values["BUS_PORT"] != want {
				t.Errorf("BUS_PORT = %q, want %q", values["BUS_PORT"], want)
			}
		})
	}
	for name, url := range map[string]string{
		"missing":       "",
		"remote ip":     "nats://10.0.0.5:4222",
		"remote name":   "nats://bus.example.invalid:4222",
		"no port":       "nats://127.0.0.1",
		"port zero":     "nats://127.0.0.1:0",
		"port too big":  "nats://127.0.0.1:65536",
		"port not num":  "nats://127.0.0.1:abc",
		"not a url":     "127.0.0.1 4222",
		"userinfo host": "nats://127.0.0.1:4222@203.0.113.9:4222",
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			r := newBusRig(t, busProfile)
			if url == "" {
				delete(r.env, "NATS_URL")
			} else {
				r.env["NATS_URL"] = url
			}
			_, _, _, fail := r.wrapBus(t)
			if fail == nil || fail.reason != RefusalSandboxHarness {
				t.Fatalf("fail = %+v, want a refusal with reason %q", fail, RefusalSandboxHarness)
			}
		})
	}
}

// The pass file must be an absolute regular file, not a link, inside the
// private token directory, and a profile that names it without one is refused.
func TestSandboxBusPassFileIsCheckedAndRefusedWhenUnsafe(t *testing.T) {
	t.Parallel()
	mk := func(t *testing.T, r *busRig) map[string]string {
		outside, _ := filepath.EvalSymlinks(t.TempDir())
		other := filepath.Join(outside, "other.pass")
		writeFile(t, other, "scratch\n", 0o600)
		link := filepath.Join(r.mgr.TokenDir, "link.pass")
		if err := os.Symlink(r.passFile, link); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(r.mgr.TokenDir, "dir.pass")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		return map[string]string{
			"relative":        "seat.pass",
			"missing file":    filepath.Join(r.mgr.TokenDir, "absent.pass"),
			"a symlink":       link,
			"a directory":     dir,
			"outside tokens":  other,
			"dotdot":          r.mgr.TokenDir + "/../" + filepath.Base(r.mgr.TokenDir) + "/seat.pass", // filepath.Join would clean it
			"not set (empty)": "",
		}
	}
	probe := newBusRig(t, busProfile)
	for name, path := range mk(t, probe) {
		t.Run("refuses "+name, func(t *testing.T) {
			r := newBusRig(t, busProfile)
			mk(t, r)
			if path == "" {
				delete(r.env, "DIRECTOR_NATS_PASS_FILE")
			} else {
				r.env["DIRECTOR_NATS_PASS_FILE"] = strings.ReplaceAll(path, probe.mgr.TokenDir, r.mgr.TokenDir)
			}
			_, _, _, fail := r.wrapBus(t)
			if fail == nil || fail.reason != RefusalSandboxHarness {
				t.Fatalf("fail = %+v, want a refusal with reason %q", fail, RefusalSandboxHarness)
			}
		})
	}
	t.Run("refuses with no token directory", func(t *testing.T) {
		r := newBusRig(t, busProfile)
		r.mgr.TokenDir = ""
		_, _, _, fail := r.wrapBus(t)
		if fail == nil || fail.reason != RefusalSandboxHarness {
			t.Fatalf("fail = %+v, want a refusal with reason %q", fail, RefusalSandboxHarness)
		}
	})
}

// "Names the shim" is decided from the profile's code: a mention in a comment
// does not count, and the spelling may vary in its whitespace (#1494 review).
func TestSandboxProfileNamesAParameterOnlyInCode(t *testing.T) {
	t.Parallel()
	t.Run("a comment does not name it", func(t *testing.T) {
		r, _ := shimRig(t, "(version 1)\n; the shim is (param \"DIRECTOR_MCP_BIN\"), exec'd in the next profile\n(deny default)\n")
		r.mgr.LookPath = func(string) (string, error) { return "", errors.New("not found") }
		if err := r.mgr.checkSandbox(r.session()); err != nil {
			t.Fatalf("checkSandbox = %v, want nil: the name is only in a comment", err)
		}
		names, _ := wrappedParams(t, r)
		if strings.Join(names, ",") != "HARNESS_BIN,MARVEL_BIN,REALHOME,SESSION_DIR,WORKDIR" {
			t.Errorf("-D parameters = %v, want the five", names)
		}
	})
	t.Run("a quoted semicolon is not a comment", func(t *testing.T) {
		r, shim := shimRig(t, "(version 1)\n(allow file-read* (literal \"/a;b\")) (allow process-exec (literal (param \"DIRECTOR_MCP_BIN\")))\n(deny default)\n")
		r.mgr.LookPath = func(name string) (string, error) {
			if name == "director-mcp" {
				return shim, nil
			}
			return "", errors.New("not found")
		}
		names, _ := wrappedParams(t, r)
		if !strings.Contains(strings.Join(names, ","), "DIRECTOR_MCP_BIN") {
			t.Errorf("-D parameters = %v, want DIRECTOR_MCP_BIN: the semicolon is inside a string", names)
		}
	})
	t.Run("extra whitespace still names it", func(t *testing.T) {
		r, shim := shimRig(t, "(version 1)\n(deny default)\n(allow process-exec (literal (param   \"DIRECTOR_MCP_BIN\" )))\n")
		r.mgr.LookPath = func(name string) (string, error) {
			if name == "director-mcp" {
				return shim, nil
			}
			return "", errors.New("not found")
		}
		names, _ := wrappedParams(t, r)
		if !strings.Contains(strings.Join(names, ","), "DIRECTOR_MCP_BIN") {
			t.Errorf("-D parameters = %v, want DIRECTOR_MCP_BIN", names)
		}
	})
}

// The decision is made from the copy that was staged, the bytes sandbox-exec
// reads, not from an earlier read of the source: a profile whose first read
// named nothing and whose staged copy names the shim, on a host without it, is
// refused at the wrap and not started with a parameter missing.
func TestSandboxWrapDecidesFromTheStagedCopy(t *testing.T) {
	t.Parallel()
	r, _ := shimRig(t, "(version 1)\n(deny default)\n(allow process-exec (literal (param \"DIRECTOR_MCP_BIN\")))\n")
	r.mgr.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	sess := r.session()
	staged, fail := r.mgr.stageSandboxProfile(sess, &sandboxLaunch{profile: r.profile, harnessBin: r.harness, marvelBin: "/m/marvel"})
	if fail != nil {
		t.Fatal(fail.msg)
	}
	plan := launchPlan{command: "'/m/marvel' 'x'", env: map[string]string{}, sandbox: staged}
	got := r.mgr.wrapInSandbox(sess, &plan)
	if got == nil || got.reason != RefusalSandboxHarness {
		t.Fatalf("wrap = %+v, want a refusal with reason %q: the staged copy names the shim and none was found", got, RefusalSandboxHarness)
	}
}

// A pass file reached through a symlinked directory is handed to the profile by
// its resolved path, the one the sandbox matches a literal against.
func TestSandboxBusPassFileIsPassedResolved(t *testing.T) {
	t.Parallel()
	r := newBusRig(t, busProfile)
	via := filepath.Join(t.TempDir(), "tokens-link")
	if err := os.Symlink(r.mgr.TokenDir, via); err != nil {
		t.Fatal(err)
	}
	r.env["DIRECTOR_NATS_PASS_FILE"] = filepath.Join(via, "seat.pass")
	_, values, _, fail := r.wrapBus(t)
	if fail != nil {
		t.Fatalf("refused: %s", fail.msg)
	}
	if values["BUS_PASS_FILE"] != r.passFile {
		t.Errorf("BUS_PASS_FILE = %q, want the resolved path %q", values["BUS_PASS_FILE"], r.passFile)
	}
}

// A state directory that already exists with a relaxed mode is made private.
func TestSandboxShimStateDirectoryIsMadePrivate(t *testing.T) {
	t.Parallel()
	r := newBusRig(t, busProfile)
	state := filepath.Join(r.home, "director-state")
	if err := os.Mkdir(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(state, 0o755); err != nil { // the process umask may have narrowed the mkdir
		t.Fatal(err)
	}
	if _, _, _, fail := r.wrapBus(t); fail != nil {
		t.Fatal(fail.msg)
	}
	if info, err := os.Stat(state); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("state directory mode = %v, %v, want 0700", info, err)
	}
}

// A state directory that is a link is refused, not followed.
func TestSandboxShimStateDirectoryThatIsALinkIsRefused(t *testing.T) {
	t.Parallel()
	r := newBusRig(t, busProfile)
	if err := os.Symlink(t.TempDir(), filepath.Join(r.home, "director-state")); err != nil {
		t.Fatal(err)
	}
	if _, _, _, fail := r.wrapBus(t); fail == nil || fail.reason != RefusalSandboxHarness {
		t.Fatalf("fail = %+v, want a refusal with reason %q", fail, RefusalSandboxHarness)
	}
}

// The profile denies four other local service ports after the bus allow, and the
// last match wins, so a NATS_URL on one of them would be allowed and then
// cancelled. The launch refuses those ports; the list is the profile's own.
func TestSandboxBusPortOnAnotherServicesPortIsRefused(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../examples/sandbox/opencode-local-bus.sb")
	if err != nil {
		t.Fatal(err)
	}
	denied := regexp.MustCompile(`\(deny network-outbound \(remote ip "localhost:(\d+)"\)\)`).FindAllStringSubmatch(string(raw), -1)
	if len(denied) < 4 {
		t.Fatalf("the profile denies %d ports, want at least four", len(denied))
	}
	for _, m := range denied {
		port := m[1]
		t.Run(port, func(t *testing.T) {
			r := newBusRig(t, string(raw)) // the example profile itself: its belts are what is refused
			r.env["NATS_URL"] = "nats://127.0.0.1:" + port
			_, _, _, fail := r.wrapBus(t)
			if fail == nil || fail.reason != RefusalSandboxHarness {
				t.Fatalf("fail = %+v, want a refusal with reason %q: the profile denies that port after the allow", fail, RefusalSandboxHarness)
			}
		})
	}
}

// The shim's state directory is opened by handle and without following a link,
// so a link swapped in between the check and the chmod is never chmodded
// (#1497 review, Low 1). The swap has no seam in the launch to hold it open, so
// the tests pin the property the fix gives: the open itself refuses a link and a
// file, and a chmod through the handle reaches the directory that was opened.
func TestOpenPrivateDirChmodsThroughTheHandle(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := openPrivateDir(dir)
	if err != nil {
		t.Fatalf("openPrivateDir = %v, want a handle on the directory", err)
	}
	defer func() { _ = f.Close() }()
	if err := f.Chmod(0o700); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("directory mode = %v, %v, want 0700 after a chmod through the handle", info, err)
	}
}

func TestOpenPrivateDirRefusesALinkAndAFile(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(base, "file")
	writeFile(t, file, "x", 0o600)
	for name, path := range map[string]string{"a link to a directory": link, "a regular file": file} {
		f, err := openPrivateDir(path)
		if err == nil {
			_ = f.Close()
			t.Errorf("openPrivateDir(%s) succeeded, want a refusal", name)
		}
	}
}

// The ports the launch refuses are the ones the staged profile denies after its
// allow, not a fixed list (#1497 review, Low 3): a profile with other belts has
// other refused ports.
func TestSandboxBusPortRefusalFollowsTheStagedProfilesDenies(t *testing.T) {
	t.Parallel()
	custom := busProfile +
		"(deny network-outbound (remote ip \"localhost:9001\"))\n" +
		"(deny network-outbound (remote ip \"localhost:9002\"))\n" +
		"(deny network* (remote ip \"localhost:9004\"))\n" +
		"; (deny network-outbound (remote ip \"localhost:9003\")) is a comment\n"
	for port, refused := range map[string]bool{"9001": true, "9002": true, "9004": true, "9003": false, "4242": false, "4333": false} {
		t.Run(port, func(t *testing.T) {
			r := newBusRig(t, custom)
			r.env["NATS_URL"] = "nats://127.0.0.1:" + port
			_, values, _, fail := r.wrapBus(t)
			switch {
			case refused && (fail == nil || fail.reason != RefusalSandboxHarness):
				t.Fatalf("fail = %+v, want a refusal: the profile denies localhost:%s", fail, port)
			case !refused && fail != nil:
				t.Fatalf("refused: %s", fail.msg)
			case !refused && values["BUS_PORT"] != port:
				t.Errorf("BUS_PORT = %q, want %q", values["BUS_PORT"], port)
			}
		})
	}
}

// A form written with no space between param and the quote is not the form:
// sandbox-exec reads param"X" as one unbound symbol and rejects the profile
// ("unbound variable: param\"BUS_PASS_FILE\"", measured in scratch), so it is
// never counted as naming the parameter and the profile's own error shows.
func TestSandboxProfileWithNoSpaceBeforeTheQuoteDoesNotNameTheParameter(t *testing.T) {
	t.Parallel()
	r := newBusRig(t, "(version 1)\n(deny default)\n(allow process-exec (literal (param\"DIRECTOR_MCP_BIN\")))\n")
	names, _, _, fail := r.wrapBus(t)
	if fail != nil {
		t.Fatal(fail.msg)
	}
	if strings.Join(names, ",") != "HARNESS_BIN,MARVEL_BIN,REALHOME,SESSION_DIR,WORKDIR" {
		t.Errorf("-D parameters = %v, want the five", names)
	}
}
