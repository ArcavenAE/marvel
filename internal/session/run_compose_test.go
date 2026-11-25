package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	netAllow  = `(allow network* (remote ip "localhost:*"))`
	bindAllow = `(allow network-bind (local ip "localhost:*"))`
	inAllow   = `(allow network-inbound (local ip "localhost:*"))`
	testBlock = "(deny file-read* (subpath \"/Volumes\"))\n"
	// allowRole is the smallest role profile a run accepts: one that names its own
	// (allow default). A role with no profile is refused.
	allowRole = "(version 1)\n(allow default)\n"
)

// Order: role, then the request, then the deny block, one text, block last.
func TestComposeRunProfileOrdersRoleRequestBlock(t *testing.T) {
	role := "(version 1)\n(deny file-write* (subpath \"/ROLE\"))\n"
	req := "(deny file-write* (subpath \"/REQUEST\"))\n"
	got, err := ComposeRunProfile(role, req, testBlock)
	if err != nil {
		t.Fatalf("a deny-only request was refused: %v", err)
	}
	r, q, b := strings.Index(got, "/ROLE"), strings.Index(got, "/REQUEST"), strings.Index(got, "/Volumes")
	if r < 0 || q < 0 || b < 0 || r >= q || q >= b {
		t.Fatalf("order is role=%d request=%d block=%d, want 0 <= role < request < block\n%s", r, q, b, got)
	}
	if !strings.HasSuffix(got, testBlock) {
		t.Fatalf("the deny block is not last:\n%s", got)
	}
}

// Director ruling (marvel#1601): a role with no sandbox_profile is refused, not
// given an (allow default) base, so no seat can leave its harness layer through
// the endpoint before the allowlist exists. A request profile that is empty,
// over a role that names its own profile, still composes.
func TestComposeRunProfileRefusesARoleWithNoProfile(t *testing.T) {
	for name, role := range map[string]string{"empty": "", "blank": " \n\t", "comment only": "; nothing here\n"} {
		_, err := ComposeRunProfile(role, "(deny network*)\n", testBlock)
		if !errors.Is(err, ErrRunProfileRefused) || !strings.Contains(err.Error(), "sandbox_profile") {
			t.Errorf("%s role: err = %v, want ErrRunProfileRefused naming sandbox_profile", name, err)
		}
		if _, err := ComposeRunProfile(role, "", testBlock); !errors.Is(err, ErrRunProfileRefused) {
			t.Errorf("%s role with an empty request: err = %v, want ErrRunProfileRefused", name, err)
		}
	}
}

func TestComposeRunProfileAcceptsAnEmptyRequestOverARoleWithAProfile(t *testing.T) {
	got, err := ComposeRunProfile("(version 1)\n(deny network*)\n", "", testBlock)
	if err != nil {
		t.Fatalf("an empty request over a role with a profile was refused: %v", err)
	}
	if strings.Count(got, "(version 1)") != 1 || strings.Contains(got, "(allow default)") || !strings.HasSuffix(got, testBlock) {
		t.Fatalf("composed text is not the role's profile then the block, with no base added:\n%s", got)
	}
}

// What the run conditions send is accepted: denies, comments, and exactly the
// three localhost network allows, in the form's own order.
func TestComposeRunProfileAcceptsTheRunConditionsForm(t *testing.T) {
	req := "; run conditions\n(deny network*)\n" + netAllow + "\n" + bindAllow + "\n" + inAllow +
		"\n(deny network-outbound (remote ip \"localhost:4222\"))\n(deny file-write* (require-not (subpath \"/private/tmp/x\")))\n"
	got, err := ComposeRunProfile(allowRole, req, testBlock)
	if err != nil {
		t.Fatalf("the run-conditions form was refused: %v", err)
	}
	for _, want := range []string{netAllow, bindAllow, inAllow, "localhost:4222"} {
		if !strings.Contains(got, want) {
			t.Errorf("composed profile lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "run conditions") {
		t.Errorf("a request comment survived re-serialization:\n%s", got)
	}
	if n, m := strings.Index(got, "localhost:4222"), strings.Index(got, netAllow); n < m {
		t.Errorf("the per-port deny (%d) came before the network allow (%d); order must be kept", n, m)
	}
}

// R3, R4: every request outside the closed grammar is refused, with a refusal
// that wraps ErrRunProfileRefused. Nothing is composed from a refused request.
func TestComposeRunProfileRefusesWhatIsNotDenyOrTheNetworkForm(t *testing.T) {
	for name, req := range map[string]string{
		"allow file-read":      `(allow file-read* (subpath "/Volumes"))`,
		"allow file-write":     `(allow file-write* (subpath "/"))`,
		"allow default":        `(allow default)`,
		"allow process-exec":   `(allow process-exec (literal "/bin/sh"))`,
		"allow mach-lookup":    `(allow mach-lookup)`,
		"allow network any":    `(allow network*)`,
		"allow network remote": `(allow network* (remote ip "1.1.1.1:*"))`,
		"allow network local":  `(allow network* (local ip "localhost:*"))`,
		"allow network out":    `(allow network-outbound (remote ip "localhost:*"))`,
		"define":               `(define (deny . a) #t)`,
		"import":               `(import "system.sb")`,
		"load":                 `(load "/tmp/x.sb")`,
		"version":              `(version 2)`,
		"param":                `(deny file-read* (subpath (param "HOME")))`,
		"unterminated form":    `(deny network* (require-any`,
		"unterminated string":  `(deny file-read* (subpath "/x))`,
		"extra close":          `(deny network*))`,
		"bare atom":            `deny`,
		"string-append":        `(deny file-read* (subpath (string-append "/a" "/b")))`,
	} {
		got, err := ComposeRunProfile(allowRole, req+"\n", testBlock)
		if !errors.Is(err, ErrRunProfileRefused) {
			t.Errorf("%s: err = %v, want ErrRunProfileRefused", name, err)
		}
		if got != "" {
			t.Errorf("%s: a refused request composed %q", name, got)
		}
	}
}

// R5: a role profile that denies network operations is not widened by the
// request's network allow forms. The role could not have asked for them.
func TestComposeRunProfileRefusesNetworkAllowsOverARoleNetworkRule(t *testing.T) {
	role := "(version 1)\n(deny default)\n(deny network-outbound (remote ip \"localhost:4222\"))\n"
	for _, req := range []string{netAllow, bindAllow, inAllow} {
		if _, err := ComposeRunProfile(role, req+"\n", testBlock); !errors.Is(err, ErrRunProfileRefused) {
			t.Errorf("%s over a role with network rules: err = %v, want ErrRunProfileRefused", req, err)
		}
	}
	// G2: a role that names network only through allows is also not widened.
	allowOnly := "(version 1)\n(deny default)\n(allow network-outbound (remote ip \"localhost:11434\"))\n"
	for _, req := range []string{netAllow, bindAllow, inAllow} {
		if _, err := ComposeRunProfile(allowOnly, req+"\n", testBlock); !errors.Is(err, ErrRunProfileRefused) {
			t.Errorf("%s over a role with only network allows: err = %v, want ErrRunProfileRefused", req, err)
		}
	}
	// V1: a deny-default role with no network rule is not given localhost.
	denyDefault := "(version 1)\n(deny default)\n(allow file-read* (subpath \"/usr\"))\n(allow process-exec (literal \"/bin/sh\"))\n"
	for _, req := range []string{netAllow, bindAllow, inAllow} {
		if _, err := ComposeRunProfile(denyDefault, req+"\n", testBlock); !errors.Is(err, ErrRunProfileRefused) {
			t.Errorf("%s over a (deny default) role with no network rule: err = %v, want ErrRunProfileRefused", req, err)
		}
	}
	// A role of a bare version form, which names neither default, is refused too.
	if _, err := ComposeRunProfile("(version 1)\n", netAllow+"\n", testBlock); !errors.Is(err, ErrRunProfileRefused) {
		t.Errorf("the network form over a role that names no (allow default): err = %v, want ErrRunProfileRefused", err)
	}
	// A role that names no network operation is not widened by the same form.
	if _, err := ComposeRunProfile("(version 1)\n(allow default)\n", netAllow+"\n", testBlock); err != nil {
		t.Errorf("the network form over a role with no network rules was refused: %v", err)
	}
	// Denies are always fine over a role with network rules.
	if _, err := ComposeRunProfile(role, "(deny network*)\n", testBlock); err != nil {
		t.Errorf("a deny over a role with network rules was refused: %v", err)
	}
}

// R6: the composed run carries the -D set the role's own launch uses.
func TestRunSandboxParamsCarryEveryParamTheRoleNames(t *testing.T) {
	role := "(version 1)\n(allow file-read* (subpath (param \"SESSION_DIR\")))\n(allow network* (remote ip (string-append \"localhost:\" (param \"BUS_PORT\"))))\n"
	got, err := RunSandboxParams(role, map[string]string{"SESSION_DIR": "/s", "BUS_PORT": "4222", "UNUSED": "x"})
	if err != nil {
		t.Fatalf("RunSandboxParams: %v", err)
	}
	if strings.Join(got, " ") != "-D BUS_PORT=4222 -D SESSION_DIR=/s" {
		t.Fatalf("params = %q, want the two named, sorted, each as -D KEY=VALUE", got)
	}
	if _, err := RunSandboxParams(role, map[string]string{"SESSION_DIR": "/s"}); err == nil {
		t.Fatal("a named parameter with no value was accepted")
	}
}

// R8: no fail-open. A host that cannot sandbox refuses the run.
func TestCheckRunHostRefusesWhatCannotSandbox(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sandbox-exec")
	if err := os.WriteFile(file, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CheckRunHost("linux", file); err == nil {
		t.Error("linux was accepted")
	}
	if err := CheckRunHost("darwin", filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("a missing sandbox-exec was accepted")
	}
	if err := CheckRunHost("darwin", t.TempDir()); err == nil {
		t.Error("a directory at the sandbox-exec path was accepted")
	}
	if err := CheckRunHost("darwin", file); err != nil {
		t.Errorf("darwin with sandbox-exec present was refused: %v", err)
	}
}

// A role holding both defaults is read as deny-default: the network allows are
// refused, never accepted on the strength of the (allow default) alone.
func TestComposeRunProfileRefusesNetworkAllowsOverARoleNamingBothDefaults(t *testing.T) {
	both := "(version 1)\n(allow default)\n(deny default)\n"
	if _, err := ComposeRunProfile(both, netAllow+"\n", testBlock); !errors.Is(err, ErrRunProfileRefused) {
		t.Errorf("err = %v, want ErrRunProfileRefused", err)
	}
}

// The composed text rides in argv, so a profile over the bound is refused with a
// reason at compose time.
func TestComposeRunProfileRefusesAProfileTooLargeForArgv(t *testing.T) {
	role := "(version 1)\n(allow default)\n;" + strings.Repeat("x", MaxComposedRunProfileBytes) + "\n(deny network*)\n"
	_, err := ComposeRunProfile(role, "(deny process-fork)\n", testBlock)
	if !errors.Is(err, ErrRunProfileRefused) || !strings.Contains(err.Error(), "argv") {
		t.Errorf("err = %v, want ErrRunProfileRefused naming argv", err)
	}
}

// A deny default with a modifier is still a deny default: a role that names
// both defaults, the deny with `(with no-log)`, is read as deny-default and the
// network allows are refused (marvel#1601 review, gap 1).
func TestComposeRunProfileReadsADenyDefaultWithAModifierAsDenyDefault(t *testing.T) {
	role := "(version 1)\n(allow default)\n(deny default (with no-log))\n"
	if _, err := ComposeRunProfile(role, netAllow+"\n", testBlock); !errors.Is(err, ErrRunProfileRefused) {
		t.Errorf("err = %v, want ErrRunProfileRefused for a role holding a modified deny default", err)
	}
}

// The text of a string literal is not a form: `(allow default)` inside a path
// literal must not make a role count as allow-default (marvel#1601 review,
// gap 2).
func TestComposeRunProfileIgnoresADefaultFormInsideAStringLiteral(t *testing.T) {
	role := "(version 1)\n(deny file-read* (literal \"/tmp/(allow default)\"))\n"
	if _, err := ComposeRunProfile(role, netAllow+"\n", testBlock); !errors.Is(err, ErrRunProfileRefused) {
		t.Errorf("err = %v, want ErrRunProfileRefused for a role whose only (allow default) is inside a literal", err)
	}
}
