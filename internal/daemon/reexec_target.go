package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// reexecParams is what `reexec` may carry. ExecPath names the binary to exec
// instead of the daemon's own path, which is how an upgrade under an install
// that keeps each version in its own directory (mise) reaches the new build:
// the client that was just installed names itself (marvel#592).
type reexecParams struct {
	ExecPath string `json:"exec_path,omitempty"`
}

// reexecVersionTimeout bounds the `<path> version` run that proves the target
// is a marvel binary before the daemon detaches for it.
var reexecVersionTimeout = 5 * time.Second

// versionLine is the first line `marvel version` prints.
var versionLine = regexp.MustCompile(`^marvel (\S+) \((\S+)\)`)

// errNotLocal is the refusal of an exec_path over mrvl://.
var errNotLocal = errors.New("exec_path is only accepted on the local unix socket: a path from a remote client names a file on another host")

// reexecTarget is a validated exec_path.
type reexecTarget struct {
	Path    string
	Version string
	Channel string
}

// validateReexecTarget resolves path and checks it is safe to exec: a regular,
// executable file owned by the daemon's user that no one else can write, that
// answers `version` as a marvel, and that is not older than the running build.
// Nothing here detaches or execs, so a refusal leaves the daemon serving.
func validateReexecTarget(path, runningVersion string) (reexecTarget, error) {
	if !filepath.IsAbs(path) {
		return reexecTarget{}, fmt.Errorf("exec_path %q is not an absolute path", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return reexecTarget{}, fmt.Errorf("exec_path %q: %w", path, err)
	}
	fi, err := os.Stat(resolved)
	if err != nil {
		return reexecTarget{}, fmt.Errorf("exec_path %q: %w", resolved, err)
	}
	if !fi.Mode().IsRegular() {
		return reexecTarget{}, fmt.Errorf("exec_path %q is not a regular file", resolved)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		return reexecTarget{}, fmt.Errorf("exec_path %q is not executable", resolved)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Getuid() {
		return reexecTarget{}, fmt.Errorf("exec_path %q is not owned by the daemon's user", resolved)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return reexecTarget{}, fmt.Errorf("exec_path %q is writable by its group or by others", resolved)
	}

	ctx, cancel := context.WithTimeout(context.Background(), reexecVersionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, resolved, "version")
	// A killed script can leave a child holding the output pipe open.
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return reexecTarget{}, fmt.Errorf("exec_path %q: running `version` failed: %w", resolved, err)
	}
	first, _, _ := strings.Cut(string(out), "\n")
	m := versionLine.FindStringSubmatch(first)
	if m == nil {
		return reexecTarget{}, fmt.Errorf("exec_path %q: `version` did not print a marvel version line", resolved)
	}
	t := reexecTarget{Path: resolved, Version: m[1], Channel: m[2]}
	if older, known := versionOlder(t.Version, runningVersion); known && older {
		return reexecTarget{}, fmt.Errorf("exec_path %q is %s, older than the running daemon %s; a downgrade is refused",
			resolved, t.Version, runningVersion)
	}
	return t, nil
}

// versionOlder reports whether a is older than b, and whether the order is
// known at all. Both must parse as semantic versions (a dotted numeric core
// with an optional dash prerelease); a dev build or anything else has no order.
func versionOlder(a, b string) (older, known bool) {
	ac, ap, aok := splitVersion(a)
	bc, bp, bok := splitVersion(b)
	if !aok || !bok {
		return false, false
	}
	for i := 0; i < len(ac) || i < len(bc); i++ {
		var x, y int
		if i < len(ac) {
			x = ac[i]
		}
		if i < len(bc) {
			y = bc[i]
		}
		if x != y {
			return x < y, true
		}
	}
	switch {
	case ap == "" && bp == "":
		return false, true
	case ap == "":
		return false, true
	case bp == "":
		return true, true
	}
	return comparePrerelease(ap, bp) < 0, true
}

// splitVersion parses "v1.2.3-pre.1+meta" into its numeric core and the
// prerelease. Build metadata is dropped.
func splitVersion(v string) (core []int, pre string, ok bool) {
	v = strings.TrimPrefix(v, "v")
	v, _, _ = strings.Cut(v, "+")
	c, pre, _ := strings.Cut(v, "-")
	if c == "" {
		return nil, "", false
	}
	for _, p := range strings.Split(c, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, "", false
		}
		core = append(core, n)
	}
	return core, pre, true
}

// comparePrerelease orders two prerelease strings as semver does: dotted
// identifiers left to right, numbers numerically and below words, a shorter
// run below a longer one that begins the same.
func comparePrerelease(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aerr := strconv.Atoi(as[i])
		bn, berr := strconv.Atoi(bs[i])
		switch {
		case aerr == nil && berr == nil:
			if an != bn {
				return cmpInt(an, bn)
			}
		case aerr == nil:
			return -1
		case berr == nil:
			return 1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	return cmpInt(len(as), len(bs))
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
