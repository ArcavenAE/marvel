package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/arcavenae/marvel/internal/api"
)

// RunPlan is a staged run: the sandbox-exec argv prefix (program, -f, the staged
// profile and the -D set), the composed text it holds, and the file to remove
// once the run ends.
type RunPlan struct {
	Prefix   []string
	Composed string
	staged   string
}

// Remove deletes the staged profile. It is safe to call twice.
func (p *RunPlan) Remove() {
	if p != nil && p.staged != "" {
		_ = os.Remove(p.staged)
		p.staged = ""
	}
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

// PrepareRun composes and stages the profile for one run of sess. The deny
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
	dir, err := m.sandboxDir()
	if err != nil {
		return nil, fmt.Errorf("the profile directory is not known: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("the profile directory cannot be made: %w", err)
	}
	name := "run-" + strings.ReplaceAll(sess.Key(), "/", "-") + "-" + strconv.FormatInt(m.now().UnixNano(), 36) + ".sb"
	path := filepath.Join(dir, name)
	if strings.ContainsAny(path, " \t\r\n") {
		return nil, fmt.Errorf("marvel's profile directory path holds whitespace")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("the profile cannot be staged: %w", err)
	}
	_, werr := f.WriteString(composed)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("the profile cannot be staged")
	}
	prefix := append([]string{m.sandboxExecPath(), "-f", path}, args...)
	return &RunPlan{Prefix: prefix, Composed: composed, staged: path}, nil
}
