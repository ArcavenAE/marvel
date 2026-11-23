package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SeatEnv is what the daemon does to the environment of every seat pane it
// starts, declared once on the daemon's own cluster entry (seat_env:). It is
// host policy, so it never rides in a workspace manifest.
type SeatEnv struct {
	// PathPrepend names directories put in front of PATH in the pane. A leading
	// ~ is the daemon user's home. A role that sets PATH itself keeps its own.
	PathPrepend []string `yaml:"path_prepend,omitempty"`
	// Unset names variables removed from the pane, beside the session
	// credentials the daemon always removes.
	Unset []string `yaml:"unset,omitempty"`
	// Secrets maps the name a role declares it needs (seat_secrets) to the
	// environment variable that carries the value in a seat. The daemon keeps the
	// variable out of the tmux server and out of every pane that does not
	// declare it; a manifest names a need and never a value. Left out, it maps bd
	// to BEADS_DOLT_PASSWORD; an empty map (secrets: {}) opts out.
	Secrets map[string]string `yaml:"secrets,omitempty"`
}

// ErrInvalidSeatEnv marks a seat_env block that names something that cannot be
// a variable name or a directory on PATH. Like ErrInvalidService it rides beside
// the parsed config, and the daemon reads the cluster with no seat_env.
var ErrInvalidSeatEnv = errors.New("invalid seat_env")

// envNameRE is a portable variable name: what `env -u` and a shell accept.
var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// seatNeedRE is the name a role gives a secret it needs: seat_secrets: [bd].
var seatNeedRE = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// SeatEnvSpec is the cluster's seat_env block, checked and with ~ expanded. A
// nil cluster or an unset block is the zero value.
func (c *Cluster) SeatEnvSpec() (SeatEnv, error) {
	if c == nil || c.SeatEnv == nil {
		return SeatEnv{Secrets: defaultSeatSecrets()}, nil
	}
	var out SeatEnv
	var errs []error
	for _, n := range c.SeatEnv.Unset {
		if !envNameRE.MatchString(n) {
			errs = append(errs, fmt.Errorf("%w: cluster %q: seat_env.unset: %q is not a variable name (letters, digits and _, not starting with a digit)", ErrInvalidSeatEnv, c.Name, n))
			continue
		}
		out.Unset = append(out.Unset, n)
	}
	for _, p := range c.SeatEnv.PathPrepend {
		dir, err := seatEnvDir(p)
		if err != nil {
			errs = append(errs, fmt.Errorf("%w: cluster %q: seat_env.path_prepend: %q: %w", ErrInvalidSeatEnv, c.Name, p, err))
			continue
		}
		out.PathPrepend = append(out.PathPrepend, dir)
	}
	needs := make([]string, 0, len(c.SeatEnv.Secrets))
	for need := range c.SeatEnv.Secrets {
		needs = append(needs, need)
	}
	sort.Strings(needs)
	byVar := map[string]string{}
	for _, need := range needs {
		name := c.SeatEnv.Secrets[need]
		// The text names the need and never the value: a mapping that holds a
		// secret where a variable name belongs must not be echoed into a log.
		if !seatNeedRE.MatchString(need) {
			errs = append(errs, fmt.Errorf("%w: cluster %q: seat_env.secrets: need %q is not a name (lower case letters, digits, - and _, starting with a letter)", ErrInvalidSeatEnv, c.Name, need))
			continue
		}
		if !envNameRE.MatchString(name) {
			errs = append(errs, fmt.Errorf("%w: cluster %q: seat_env.secrets.%s: the value is not a variable name", ErrInvalidSeatEnv, c.Name, need))
			continue
		}
		if other, dup := byVar[name]; dup {
			errs = append(errs, fmt.Errorf("%w: cluster %q: seat_env.secrets: needs %s and %s name the same variable", ErrInvalidSeatEnv, c.Name, other, need))
			continue
		}
		byVar[name] = need
		if out.Secrets == nil {
			out.Secrets = map[string]string{}
		}
		out.Secrets[need] = name
	}
	if len(errs) > 0 {
		return SeatEnv{}, errors.Join(errs...)
	}
	if c.SeatEnv.Secrets == nil {
		out.Secrets = defaultSeatSecrets()
	}
	return out, nil
}

// defaultSeatSecrets is the mapping a cluster gets when its seat_env says nothing
// about secrets: the bd need fills the dolt password variable, so the password
// reaches only the seats whose role declares seat_secrets: [bd]. A cluster that
// wants every seat to keep inheriting it writes secrets: {}.
func defaultSeatSecrets() map[string]string {
	return map[string]string{"bd": "BEADS_DOLT_PASSWORD"}
}

// SecretVars is the variable names the cluster maps its seat secrets to, in
// name order.
func (s SeatEnv) SecretVars() []string {
	out := make([]string, 0, len(s.Secrets))
	for _, v := range s.Secrets {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// seatEnvDir expands a leading ~ and refuses what would split or confuse PATH:
// an empty entry, a colon, a control character or a relative directory.
func seatEnvDir(p string) (string, error) {
	if p == "" {
		return "", errors.New("is empty")
	}
	if strings.ContainsAny(p, ":\x00\n\r") {
		return "", errors.New("holds a colon or a control character, which would split PATH")
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", errors.New("starts with ~ and the home directory is unknown")
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	if !filepath.IsAbs(p) {
		return "", errors.New("is not an absolute directory")
	}
	return p, nil
}
