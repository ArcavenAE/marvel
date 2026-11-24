package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
}

// ErrInvalidSeatEnv marks a seat_env block that names something that cannot be
// a variable name or a directory on PATH. Like ErrInvalidService it rides beside
// the parsed config, and the daemon reads the cluster with no seat_env.
var ErrInvalidSeatEnv = errors.New("invalid seat_env")

// envNameRE is a portable variable name: what `env -u` and a shell accept.
var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// SeatEnvSpec is the cluster's seat_env block, checked and with ~ expanded. A
// nil cluster or an unset block is the zero value.
func (c *Cluster) SeatEnvSpec() (SeatEnv, error) {
	if c == nil || c.SeatEnv == nil {
		return SeatEnv{}, nil
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
	if len(errs) > 0 {
		return SeatEnv{}, errors.Join(errs...)
	}
	return out, nil
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
