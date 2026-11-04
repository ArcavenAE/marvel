package session

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	stdruntime "runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/paths"
)

// The launch half of a sandbox-exec profile: a role that names a profile runs its
// prepared command under /usr/bin/sandbox-exec with that profile, on darwin, and
// the adapter is told the profile is in force (LaunchContext.SandboxProfile), so
// its posture rule can read what is in force and not a recommendation. The shape
// is the container placement's (container_launch.go): a refusal at spawn with no
// session record, a standing record with its own reason, one event per change,
// and the refusal ends on its own once the cause is fixed. The key name,
// sandbox_profile, was ratified by the operator on 2026-11-03.
//
// Two sites, because the profile must be known before the adapter prepares the
// launch (its posture rule reads it there) and the container wrap, the model for
// the other half, runs after:
//
//  1. checkSandbox, before the session record exists: every check, and the
//     refusal record.
//  2. planLaunch, at the point the adapter's launch context is built: the same
//     checks again, quietly, then a copy of the profile into marvel's own
//     per-launch directory. The copy is what sandbox-exec is pointed at, and its
//     sha256 is the sha256 of the bytes sandbox-exec reads. A failure here holds
//     the launch; nothing is ever started without the profile.
//
// wrapInSandbox then prefixes the planned command after planLaunch. The prefix
// is plain text in front of the command, so the pane shell opens the command's
// own redirections (the stream sink, </dev/null) outside the sandbox and the
// profile need not allow writes to marvel's run directory.

// sandboxRefusalSuffix keys the sandbox refusal apart from the directory and
// container refusals of the same role, so each keeps its own record and neither
// clears the other.
const sandboxRefusalSuffix = "#sandbox"

// defaultSandboxExec is a fixed path, not a PATH lookup, so a shadowing binary
// cannot stand in for the real one.
const defaultSandboxExec = "/usr/bin/sandbox-exec"

// maxSandboxProfileBytes bounds the profile marvel copies.
const maxSandboxProfileBytes = 1 << 20

// sandboxExecutable is the seam for marvel's own binary, as gooseExecutable is
// for the goose adapter.
var sandboxExecutable = os.Executable

// currentHome is the home of the user marvel runs as, read from the user
// database and not from $HOME, which a test or a wrapper may have repointed.
var currentHome = func() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return u.HomeDir, nil
}

// sandboxFailure is one reason a profiled launch cannot start.
type sandboxFailure struct {
	reason string
	msg    string
}

// sandboxLaunch is what site 1 resolved: the role's profile as named, and the
// two binaries the profile must allow to exec.
type sandboxLaunch struct {
	profile    string
	harnessBin string
	marvelBin  string
}

// sandboxPlan is what site 2 staged for one launch.
type sandboxPlan struct {
	launch   sandboxLaunch
	copyPath string
	sha      string
}

// roleOf finds the role a session belongs to.
func (m *Manager) roleOf(sess *api.Session) (api.Role, bool) {
	if m.roleReader != nil {
		return m.roleReader(sess)
	}
	team, err := m.store.GetTeam(sess.Workspace + "/" + sess.Team)
	if err != nil {
		return api.Role{}, false
	}
	for _, r := range team.Roles {
		if r.Name == sess.Role {
			return r, true
		}
	}
	return api.Role{}, false
}

func (m *Manager) sandboxExecPath() string {
	if m.SandboxExec != "" {
		return m.SandboxExec
	}
	return defaultSandboxExec
}

func (m *Manager) goos() string {
	if m.GOOS != "" {
		return m.GOOS
	}
	return stdruntime.GOOS
}

// sandboxDir is marvel's own directory for the per-launch profile copies.
func (m *Manager) sandboxDir() (string, error) {
	if m.SandboxDir != "" {
		return m.SandboxDir, nil
	}
	layout, err := paths.Default()
	if err != nil {
		return "", err
	}
	return filepath.Join(layout.RunDir(), "sandbox"), nil
}

// roleNamesSandbox reports whether sess's role names a sandbox profile. Create
// asks it once, at site 1, and keeps the answer for unwrappedProfiledLaunch: a
// seat that was profiled when it was checked is never started from a plan that
// carries no sandbox.
func (m *Manager) roleNamesSandbox(sess *api.Session) bool {
	role, ok := m.roleOf(sess)
	return ok && role.SandboxProfile != ""
}

// unwrappedProfiledLaunch is the fail-closed check after planLaunch. A seat
// that was profiled at site 1 must reach the wrap with a staged sandbox, or
// leave planLaunch with its own failure; a plan with neither is the bare
// command, which planLaunch returns when its team, role or workspace lookup
// misses, and when its role read and resolveSandbox's read disagree about the
// profile. It returns the refusal for such a plan, and nil for every other.
func (m *Manager) unwrappedProfiledLaunch(sess *api.Session, profiled bool, plan launchPlan) *sandboxFailure {
	if !profiled || plan.sandbox != nil || plan.sandboxFail != nil {
		return nil
	}
	return &sandboxFailure{
		reason: RefusalSandboxChanged,
		msg: fmt.Sprintf("not started: %s/%s named a sandbox profile when it was checked, and the planned launch carries none (its team, role or workspace changed before the launch was planned); nothing is started unsandboxed",
			sess.Team, sess.Role),
	}
}

// resolveSandbox runs every check for a profiled role and records nothing. It
// returns nil, nil for a role with no profile.
func (m *Manager) resolveSandbox(sess *api.Session) (*sandboxLaunch, *sandboxFailure) {
	role, ok := m.roleOf(sess)
	if !ok || role.SandboxProfile == "" {
		return nil, nil
	}
	fail := func(reason, format string, a ...any) (*sandboxLaunch, *sandboxFailure) {
		return nil, &sandboxFailure{reason: reason, msg: fmt.Sprintf("not started: %s/%s names a sandbox profile and %s; nothing is started without it", sess.Team, sess.Role, fmt.Sprintf(format, a...))}
	}

	if g := m.goos(); g != "darwin" {
		return fail(RefusalSandboxHost, "this host is %s, and a sandbox-exec profile runs on darwin only (remove sandbox_profile from the role)", g)
	}
	exe := m.sandboxExecPath()
	if info, err := os.Stat(exe); err != nil || !info.Mode().IsRegular() {
		return fail(RefusalSandboxHost, "%s is not present", exe)
	}

	profile := role.SandboxProfile
	if !api.ValidSandboxProfile(profile) {
		return fail(RefusalSandboxProfile, "sandbox_profile is not a clean absolute path")
	}
	info, err := os.Stat(profile)
	if err != nil || !info.Mode().IsRegular() {
		return fail(RefusalSandboxProfile, "its profile is not a regular file at the path the role names")
	}
	f, err := os.Open(profile)
	if err != nil {
		return fail(RefusalSandboxProfile, "its profile is not readable")
	}
	_ = f.Close()

	var explicit api.Placement
	if ws, err := m.store.GetWorkspace(sess.Workspace); err == nil {
		explicit = ws.Placement
	}
	placement, err := api.ResolvePlacement(explicit, m.adapters.RecommendedPlacement(sess.Runtime.Name))
	if err != nil {
		return fail(RefusalSandboxContainer, "its placement does not resolve: %v", err)
	}
	if placement == api.PlacementContainer {
		return fail(RefusalSandboxContainer, "its placement is also container; whether the two layers stack is not settled, so both together are refused (set placement = \"localhost\" or drop the profile)")
	}

	raw := sess.Runtime.Command
	if raw == "" {
		raw = sess.Runtime.Name
	}
	if issue := shapeIssue(sess.Runtime.Command, false); issue != "" && sess.Runtime.Command != "" {
		return fail(RefusalSandboxShape, "the role's command holds %s, so the profile cannot be put in front of one simple command", issue)
	}
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return fail(RefusalSandboxHarness, "the role names no harness command")
	}
	harness, err := m.resolveBinary(fields[0])
	if err != nil {
		return fail(RefusalSandboxHarness, "the harness binary %q cannot be resolved: %v", fields[0], err)
	}
	self, err := sandboxExecutable()
	if err != nil {
		return fail(RefusalSandboxHarness, "the marvel binary was not found: %v", err)
	}
	marvelBin, err := filepath.EvalSymlinks(self)
	if err != nil || !filepath.IsAbs(marvelBin) {
		return fail(RefusalSandboxHarness, "the marvel binary cannot be resolved")
	}
	return &sandboxLaunch{profile: profile, harnessBin: harness, marvelBin: marvelBin}, nil
}

// resolveBinary turns a command word into the absolute, symlink-free path of an
// executable regular file, as the profile's exec rule matches the resolved file.
func (m *Manager) resolveBinary(word string) (string, error) {
	path := word
	if !strings.Contains(word, "/") {
		found, err := m.lookPath()(word)
		if err != nil {
			return "", errors.New("not found on PATH")
		}
		path = found
	}
	if !filepath.IsAbs(path) {
		return "", errors.New("not an absolute path")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", errors.New("does not resolve")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", errors.New("not an executable regular file")
	}
	return resolved, nil
}

// checkSandbox is site 1: the checks, and the refusal record, before the session
// record exists. A role with no profile, or one whose cause is fixed, clears its
// standing record.
func (m *Manager) checkSandbox(sess *api.Session) error {
	key := sess.Workspace + "/" + sess.Team + "/" + sess.Role + sandboxRefusalSuffix
	_, fail := m.resolveSandbox(sess)
	if fail == nil {
		m.refusedMu.Lock()
		delete(m.refused, key)
		m.refusedMu.Unlock()
		return nil
	}
	return m.refuseSandbox(sess, fail)
}

// refuseSandbox records the refusal and returns the error that ends the spawn.
func (m *Manager) refuseSandbox(sess *api.Session, f *sandboxFailure) error {
	key := sess.Workspace + "/" + sess.Team + "/" + sess.Role + sandboxRefusalSuffix
	m.refusedMu.Lock()
	m.noteRefusal(sess, key, f.reason, events.KindSessionSandboxRefused, "", f.msg)
	m.refusedMu.Unlock()
	return fmt.Errorf("refuse to start session %s: %s: %s: %w", sess.Key(), f.reason, f.msg, ErrPlacementRefused)
}

// stageSandboxProfile is site 2's copy: the profile is read once, written to a
// marvel-owned per-launch file, and read back, and the hash is of the file
// sandbox-exec will read.
func (m *Manager) stageSandboxProfile(sess *api.Session, l *sandboxLaunch) (*sandboxPlan, *sandboxFailure) {
	f := func(format string, a ...any) *sandboxFailure {
		return &sandboxFailure{reason: RefusalSandboxProfile, msg: fmt.Sprintf("not started: %s/%s: the profile cannot be staged for this launch: %s; nothing is started without it", sess.Team, sess.Role, fmt.Sprintf(format, a...))}
	}
	src, err := os.ReadFile(l.profile)
	if err != nil {
		return nil, f("read: %v", err)
	}
	if len(src) == 0 || len(src) > maxSandboxProfileBytes {
		return nil, f("its size is outside 1 byte to %d bytes", maxSandboxProfileBytes)
	}
	dir, err := m.sandboxDir()
	if err != nil {
		return nil, f("no directory: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, f("directory: %v", err)
	}
	base := strings.ReplaceAll(sess.Key(), "/", "-")
	name := base + "-" + strconv.FormatInt(m.now().UnixNano(), 36) + ".sb"
	copyPath := filepath.Join(dir, name)
	if strings.ContainsAny(copyPath, " \t\r\n") {
		return nil, f("marvel's profile directory path holds whitespace")
	}
	out, err := os.OpenFile(copyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, f("create: %v", err)
	}
	_, werr := out.Write(src)
	cerr := out.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(copyPath)
		return nil, f("write")
	}
	back, err := os.ReadFile(copyPath)
	if err != nil {
		_ = os.Remove(copyPath)
		return nil, f("read back: %v", err)
	}
	sum := sha256.Sum256(back)
	// An older launch's copy of the same session is done with.
	if old, err := filepath.Glob(filepath.Join(dir, base+"-*.sb")); err == nil {
		for _, p := range old {
			if p != copyPath {
				_ = os.Remove(p)
			}
		}
	}
	return &sandboxPlan{launch: *l, copyPath: copyPath, sha: hex.EncodeToString(sum[:])}, nil
}

// wrapInSandbox is the counterpart of wrapInContainer: it prefixes the planned
// command with sandbox-exec, the staged copy and the fixed -D set. Every branch
// of the plan is wrapped, so no fallback starts a profiled seat unsandboxed. The
// environment stays the pane's own.
//
// A /bin/sh refusal stub (the goose adapter's, when marvel's own binary cannot be
// found) is unreachable under a profile: resolveSandbox refuses a missing marvel
// binary first. If it were ever reached, the sandbox would kill the stub before
// it prints its reason or exits 78, which is why that check must come first.
func (m *Manager) wrapInSandbox(sess *api.Session, plan *launchPlan) *sandboxFailure {
	sb := plan.sandbox
	f := func(reason, format string, a ...any) *sandboxFailure {
		return &sandboxFailure{reason: reason, msg: fmt.Sprintf("not started: %s/%s names a sandbox profile and %s; nothing is started without it", sess.Team, sess.Role, fmt.Sprintf(format, a...))}
	}
	if issue := shapeIssue(plan.command, true); issue != "" {
		return f(RefusalSandboxShape, "its launch command holds %s, so the profile cannot be put in front of one simple command", issue)
	}
	home, work := sess.HarnessHome, sess.WorkDir
	if !filepath.IsAbs(home) || !filepath.IsAbs(work) {
		return f(RefusalSandboxHarness, "the seat's harness home or directory is not an absolute path, and the profile's write rule cannot be stated for it")
	}
	for _, tree := range []string{home, work} {
		if within(sb.copyPath, tree) {
			return f(RefusalSandboxProfile, "marvel's own profile directory sits inside a tree the seat may write (%s)", tree)
		}
	}
	realHome, err := m.realHome()
	if err != nil || !filepath.IsAbs(realHome) {
		return f(RefusalSandboxHarness, "the home of the user marvel runs as is not known")
	}
	params := map[string]string{
		"SESSION_DIR": home,
		"WORKDIR":     work,
		"MARVEL_BIN":  sb.launch.marvelBin,
		"HARNESS_BIN": sb.launch.harnessBin,
		"REALHOME":    realHome,
	}
	names := make([]string, 0, len(params))
	for k := range params {
		names = append(names, k)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString(shellQuote(m.sandboxExecPath()) + " -f " + shellQuote(sb.copyPath))
	for _, k := range names {
		b.WriteString(" -D " + shellQuote(k+"="+params[k]))
	}
	plan.command = b.String() + " " + plan.command
	return nil
}

func (m *Manager) realHome() (string, error) {
	if m.RealHome != "" {
		return m.RealHome, nil
	}
	return currentHome()
}

// within reports whether path is dir or lies under it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

var assignmentRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// shapeIssue reports why text is not one simple command with at most trailing
// redirections, in a few words, or "" when it is. It is the one check behind both
// refusals of the "sandbox shape" reason: the raw Runtime.Command at site 1
// (quotes false: a quote or a backslash is itself the issue), and the planned
// command in wrapInSandbox (quotes true: the adapters quote with single quotes
// only, so a single-quoted span may hold anything and `\'` may sit between two
// spans). Redirections (< and >) are allowed, since the prefix goes in front of
// them. Outside single quotes, a shell operator, a substitution, a double quote,
// any other backslash, a newline, a comment or a leading VAR= is an issue.
func shapeIssue(text string, quotes bool) string {
	if strings.TrimSpace(text) == "" {
		return "no command"
	}
	inSingle := false
	wordStart := true
	for i := 0; i < len(text); i++ {
		c := text[i]
		if inSingle {
			if c == '\'' {
				inSingle = false
			}
			continue
		}
		switch c {
		case '\'':
			if !quotes {
				return "a quote"
			}
			inSingle = true
		case '\\':
			if !quotes || i+1 >= len(text) || text[i+1] != '\'' {
				return "a backslash"
			}
			i++
		case '"':
			return "a double quote"
		case ';', '&', '|', '(', ')', '`', '$':
			return "the shell character " + strconv.Quote(string(c))
		case '\n', '\r':
			return "a line break"
		case '#':
			if wordStart {
				return "a comment"
			}
		}
		wordStart = c == ' ' || c == '\t'
	}
	if inSingle {
		return "an unterminated quote"
	}
	if assignmentRE.MatchString(strings.TrimSpace(text)) {
		return "a leading variable assignment"
	}
	return ""
}

// The fixed suffix session.created carries for a profiled launch. One function
// builds it and one parses it back, so the text is a record and not prose.
const sandboxEventPrefix = "; sandbox profile "

// sandboxEventSuffix is `; sandbox profile <copy path> sha256 <64 hex>`.
func sandboxEventSuffix(copyPath, sha string) string {
	return sandboxEventPrefix + copyPath + " sha256 " + sha
}

var sandboxEventRE = regexp.MustCompile(`; sandbox profile (/\S+) sha256 ([0-9a-f]{64})$`)

// ParseSandboxEventSuffix reads the suffix back from a session.created message.
func ParseSandboxEventSuffix(msg string) (copyPath, sha string, ok bool) {
	mm := sandboxEventRE.FindStringSubmatch(msg)
	if mm == nil {
		return "", "", false
	}
	return mm[1], mm[2], true
}
