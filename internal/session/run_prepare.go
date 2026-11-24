package session

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/arcavenae/marvel/internal/api"
)

// RunPlan is a composed run: the sandbox-exec argv prefix (program, -p, the
// composed profile text and the -D set) and the text it holds. The profile
// travels in argv and no file is staged, so there is nothing a seat of the same
// uid could rewrite between the write and the read. The profile carries paths,
// not secrets, so argv visibility costs little.
type RunPlan struct {
	Prefix   []string
	Composed string
}

// RoleProfileText returns the text of the profile a role names, or "" when it
// names none. A named profile that cannot be read is an error: the run is
// refused and never composed without it.
func RoleProfileText(role api.Role) (string, error) {
	if role.SandboxProfile == "" {
		return "", nil
	}
	b, err := os.ReadFile(role.SandboxProfile)
	if err != nil {
		return "", fmt.Errorf("the role's sandbox profile cannot be read: %w", err)
	}
	if len(b) > maxSandboxProfileBytes {
		return "", fmt.Errorf("the role's sandbox profile is over %d bytes", maxSandboxProfileBytes)
	}
	return string(b), nil
}

// PrepareRun composes the profile for one run of sess. The deny
// block is the seat's, with its carve-out taken from the recorded working
// directory. The -D set holds only what the seat's record can supply
// (SESSION_DIR, WORKDIR, REALHOME); a role that names any other parameter is
// refused, since this run has no launch environment to fill it from. Nothing
// here falls back to running unwrapped.
func (m *Manager) PrepareRun(sess api.Session, roleText string, req api.RunSandboxedRequest) (*RunPlan, error) {
	if err := CheckRunHost(m.goos(), m.sandboxExecPath()); err != nil {
		return nil, err
	}
	block, err := m.tccBlock(&sess)
	if err != nil {
		return nil, fmt.Errorf("the deny block cannot be built: %w", err)
	}
	composed, err := ComposeRunProfile(roleText, req.Profile, block)
	if err != nil {
		return nil, err
	}
	home, err := m.realHome()
	if err != nil || !filepath.IsAbs(home) {
		return nil, fmt.Errorf("the home of the user marvel runs as is not known")
	}
	args, err := RunSandboxParams(roleText, map[string]string{
		"SESSION_DIR": sess.HarnessHome,
		"WORKDIR":     sess.WorkDir,
		"REALHOME":    home,
	})
	if err != nil {
		return nil, err
	}
	prefix := append([]string{m.sandboxExecPath(), "-p", composed}, args...)
	return &RunPlan{Prefix: prefix, Composed: composed}, nil
}
