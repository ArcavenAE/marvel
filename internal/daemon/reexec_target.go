package daemon

import (
	"debug/buildinfo"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// reexecParams is what `reexec` may carry. ExecPath names the binary to exec
// instead of the daemon's own path, which is how an upgrade under an install
// that keeps each version in its own directory (mise) reaches the new build:
// the client that was just installed names itself (marvel#592).
type reexecParams struct {
	ExecPath string `json:"exec_path,omitempty"`
}

// marvelModule is the main module a marvel binary records. A binary built from
// anything else is refused.
const marvelModule = "github.com/arcavenae/marvel"

// stopStartPath is what an operator does when the target cannot be proven to
// be a marvel build: detach the old daemon and start the new one by hand
// (docs/admin-guide.md, "Under mise").
const stopStartPath = "stop the daemon with 'marvel stop --keep-bus' and start the new binary yourself (docs/admin-guide.md, \"Under mise\")"

// ldflagVersion and ldflagChannel pull the stamped build out of the -ldflags
// setting the toolchain records (-X main.version=... -X main.channel=...), the
// way ci.yml and .goreleaser.yml set them. -s -w strips symbols, not this.
var (
	ldflagVersion = regexp.MustCompile(`-X\s+main\.version=(\S+)`)
	ldflagChannel = regexp.MustCompile(`-X\s+main\.channel=(\S+)`)
)

// checkAncestors walks every directory from the resolved file's parent up to /,
// on the resolved path, so a symlinked component is judged where it lands. Each
// directory must be owned by root or the daemon's user and must not be group- or
// world-writable unless it is sticky: whoever can write a directory on the path
// can rename the file away and put another in its place between these checks
// and the exec, which an owner check on the file alone does not stop. A
// group-writable install directory is refused on purpose.
func checkAncestors(resolved string) error {
	dir := filepath.Dir(resolved)
	for {
		mode, uid, err := statDir(dir)
		if err != nil {
			return fmt.Errorf("exec_path %q: directory %s: %w; %s", resolved, dir, err, stopStartPath)
		}
		if uid != 0 && uid != os.Getuid() {
			return fmt.Errorf("exec_path %q: directory %s is owned by uid %d (mode %v), not by root or the daemon's user; %s",
				resolved, dir, uid, mode, stopStartPath)
		}
		if mode.Perm()&0o022 != 0 && mode&os.ModeSticky == 0 {
			return fmt.Errorf("exec_path %q: directory %s (owner uid %d, mode %v) is writable by its group or by others and is not sticky, so the file could be swapped before the exec; %s",
				resolved, dir, uid, mode, stopStartPath)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

// errNotLocal is the refusal of an exec_path over mrvl://.
var errNotLocal = errors.New("exec_path is only accepted on the local unix socket: a path from a remote client names a file on another host")

// reexecTarget is a validated exec_path.
type reexecTarget struct {
	Path     string
	Version  string
	Channel  string
	Revision string
	Modified bool
}

// statDir reads a directory's mode and owner. It is a variable so a test can
// stand in an owner this process cannot create.
var statDir = func(path string) (mode os.FileMode, uid int, err error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, fmt.Errorf("no owner recorded for %s", path)
	}
	return fi.Mode(), int(st.Uid), nil
}

// validateReexecTarget resolves path and checks it is safe to exec: a regular,
// executable file owned by the daemon's user that no one else can write, in a
// directory chain that no one else can swap it out of, that records marvel's
// main module and a stamped version, and is not older than the running build.
// The build is read from the file, never run: `marvel version` dials the
// daemon, so spawning it would be a route into the daemon's own socket.
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

	if err := checkAncestors(resolved); err != nil {
		return reexecTarget{}, err
	}

	info, err := buildinfo.ReadFile(resolved)
	if err != nil {
		return reexecTarget{}, fmt.Errorf("exec_path %q: its build information is unreadable (%v); %s",
			resolved, err, stopStartPath)
	}
	if info.Main.Path != marvelModule {
		return reexecTarget{}, fmt.Errorf("exec_path %q was not built from %s (main module %q); %s",
			resolved, marvelModule, info.Main.Path, stopStartPath)
	}
	t := reexecTarget{Path: resolved}
	for _, kv := range info.Settings {
		switch kv.Key {
		case "-ldflags":
			if m := ldflagVersion.FindStringSubmatch(kv.Value); m != nil {
				t.Version = strings.Trim(m[1], `"'`)
			}
			if m := ldflagChannel.FindStringSubmatch(kv.Value); m != nil {
				t.Channel = strings.Trim(m[1], `"'`)
			}
		case "vcs.revision":
			t.Revision = kv.Value
		case "vcs.modified":
			t.Modified = kv.Value == "true"
		}
	}
	if t.Version == "" {
		return reexecTarget{}, fmt.Errorf("exec_path %q records no stamped version (-X main.version); %s",
			resolved, stopStartPath)
	}
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
