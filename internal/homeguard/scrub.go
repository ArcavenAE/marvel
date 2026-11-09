package homeguard

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// keepEnv is the list of variables a guarded test binary keeps from the
// environment it was started with; every other variable is removed before any
// test runs, so a child a test starts inherits only these and the scratch HOME.
// It is a list of what is kept, not of what is removed, so a credential variable
// that nobody has named yet is gone too. HOME is not on it: Run sets it to the
// scratch home separately.
//
// TMUX_TMPDIR is not kept from the parent: Run sets it to a fresh scratch
// directory (see scratchTmuxDir), because a child that held the parent's value
// could reach the tmux server the operator's seats run in.
//
// GOCACHE and GOMODCACHE are kept, and set from their resolved values when the
// environment did not carry them (see resolveGoCaches), because a go command that
// a test runs finds both under HOME otherwise, and the scratch HOME is empty:
// every such test would download its modules again, and fail offline.
var keepEnv = []string{"PATH", "LANG", "TMPDIR", "GOCACHE", "GOMODCACHE"}

// goCacheNames are the variables resolveGoCaches settles, in the order go env
// prints them.
var goCacheNames = []string{"GOCACHE", "GOMODCACHE"}

// goEnvPassthrough is what the one `go env` call that resolves the caches is
// given besides PATH and the real HOME: only names that decide where go looks
// for its own configuration and caches. No credential variable is among them.
var goEnvPassthrough = []string{"GOENV", "GOPATH", "GOFLAGS", "XDG_CACHE_HOME", "XDG_CONFIG_HOME"}

// runGoEnv runs `go env GOCACHE GOMODCACHE` with env and returns its output. It
// is a variable so a test can answer without running go.
var runGoEnv = func(env []string) ([]byte, error) {
	cmd := exec.Command("go", "env", "GOCACHE", "GOMODCACHE")
	cmd.Env = env
	return cmd.Output()
}

// resolveGoCaches returns NAME=value entries for GOCACHE and GOMODCACHE as they
// are before HOME is replaced: the value in environ when it is set, and
// otherwise what `go env` prints, run once with the real HOME. When go cannot be
// run, or prints something that is not two values, it returns what it has and a
// note saying which names were left unset.
func resolveGoCaches(environ []string) (entries []string, note string) {
	have := map[string]string{}
	for _, kv := range environ {
		if name, value, ok := strings.Cut(kv, "="); ok && slices.Contains(goCacheNames, name) && value != "" {
			have[name] = value
		}
	}
	var missing []string
	for _, name := range goCacheNames {
		if _, ok := have[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		var env []string
		for _, kv := range environ {
			name, _, _ := strings.Cut(kv, "=")
			if name == "PATH" || name == "HOME" || slices.Contains(goEnvPassthrough, name) || slices.Contains(goCacheNames, name) {
				env = append(env, kv)
			}
		}
		out, err := runGoEnv(env)
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if err != nil || len(lines) != len(goCacheNames) {
			return cacheEntries(have), fmt.Sprintf("go env did not answer (%v); %s left unset", err, strings.Join(missing, " and "))
		}
		for i, name := range goCacheNames {
			if _, ok := have[name]; !ok && strings.TrimSpace(lines[i]) != "" {
				have[name] = strings.TrimSpace(lines[i])
			}
		}
	}
	return cacheEntries(have), ""
}

func cacheEntries(have map[string]string) []string {
	var out []string
	for _, name := range goCacheNames {
		if v, ok := have[name]; ok {
			out = append(out, name+"="+v)
		}
	}
	return out
}

// scrubbedEnv returns the entries of environ whose names are in keep, in their
// original order. An entry with no name or no equals sign is dropped.
func scrubbedEnv(environ, keep []string) []string {
	var out []string
	for _, kv := range environ {
		name, _, ok := strings.Cut(kv, "=")
		if ok && name != "" && slices.Contains(keep, name) {
			out = append(out, kv)
		}
	}
	return out
}

// scrubProcessEnv replaces the process environment with scrubbedEnv(environ,
// keepEnv), then sets the resolved cache entries over it. It must run before
// HOME is replaced, because the caches are resolved from the real one.
func scrubProcessEnv() error {
	environ := os.Environ()
	caches, note := resolveGoCaches(environ)
	if note != "" {
		fmt.Fprintf(os.Stderr, "homeguard: %s\n", note)
	}
	kept := append(scrubbedEnv(environ, keepEnv), caches...)
	os.Clearenv()
	for _, kv := range kept {
		name, value, _ := strings.Cut(kv, "=")
		if err := os.Setenv(name, value); err != nil {
			return err
		}
	}
	return nil
}

// tmuxSocketBudget is the room a unix socket path leaves for tmux's own
// "/tmux-<uid>/<name>" below TMUX_TMPDIR: the limit is 104 bytes on macOS, and
// the longest name a test uses (marvel-test-session-<pid>) takes about 40.
const tmuxSocketBudget = 60

// scratchTmuxDir makes the directory TMUX_TMPDIR points a guarded binary's tmux
// at: a fresh 0700 directory under the temp dir, or under /tmp when the temp dir
// is too long for a socket path to fit below it.
func scratchTmuxDir() (string, error) {
	base := os.TempDir()
	if len(base) > tmuxSocketBudget-len("mt-123456789") {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "mt-")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}
