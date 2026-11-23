package session

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/arcavenae/marvel/internal/api"
)

// The default deny of the macOS privacy folders and network volumes
// (docs/design/seat-tcc-deny.md). macOS charges a privacy request to the
// responsible process, which for every command a seat runs is the daemon, so a
// seat that walks the home folder raises a prompt in the daemon's name. A
// sandbox-exec profile that refuses those paths makes the walk fail in the
// kernel, before any prompt.
//
// It is a guard against prompts, not containment: a seat under it is not
// "sandboxed" for an adapter's posture rule, so it never sets
// LaunchContext.SandboxProfile. A failure to stage it is logged and the seat
// starts without it, since refusing every seat over a nuisance guard is worse
// than a prompt.

// tccHomeFolders are the paths under the user's home denied to a seat. The
// first group is what Apple documents as privacy-guarded. Pictures, Music and
// Movies are denied whole and are not shown to be file-level classes; OrbStack
// is the NFS share some hosts mount inside the home.
var tccHomeFolders = []string{
	"Desktop", "Documents", "Downloads",
	"Library/Mobile Documents",
	"Library/Calendars", "Library/Mail", "Library/Messages", "Library/Safari",
	"Library/Application Support/AddressBook",
	"Pictures", "Music", "Movies",
	"OrbStack",
}

// tccFixedPaths are denied wherever the home is: every external and network
// volume mounts under /Volumes.
var tccFixedPaths = []string{"/Volumes"}

// sbplPathSafe reports whether p can sit inside a quoted sandbox-exec path
// literal as it stands: absolute, with no quote, backslash or control byte.
func sbplPathSafe(p string) bool {
	if !filepath.IsAbs(p) {
		return false
	}
	for i := 0; i < len(p); i++ {
		if c := p[i]; c < 0x20 || c == 0x7f || c == '"' || c == '\\' {
			return false
		}
	}
	return true
}

// tccDenyPaths is every path the block denies: the home folders, the fixed
// paths and the network mount points, unsafe ones dropped, duplicates removed.
func tccDenyPaths(home string, mounts []string) []string {
	var all []string
	if sbplPathSafe(home) {
		for _, f := range tccHomeFolders {
			all = append(all, filepath.Join(home, f))
		}
	}
	all = append(all, tccFixedPaths...)
	all = append(all, mounts...)
	seen := map[string]bool{}
	var out []string
	for _, p := range all {
		p = filepath.Clean(p)
		if p == "/" || !sbplPathSafe(p) || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// tccDenyBlock renders the sandbox-exec rules. A path in allowBack that lies
// under a denied path is allowed again after the deny, so the later rule wins:
// a user who keeps code under ~/Documents still gets their working tree.
func tccDenyBlock(home string, mounts, allowBack []string) string {
	denied := tccDenyPaths(home, mounts)
	var b strings.Builder
	b.WriteString("; marvel: deny the macOS privacy folders and network volumes (docs/design/seat-tcc-deny.md)\n")
	b.WriteString("(deny file-read* file-write*")
	for _, p := range denied {
		fmt.Fprintf(&b, "\n  (subpath \"%s\")", p)
	}
	b.WriteString(")\n")
	for _, a := range allowBack {
		a = filepath.Clean(a)
		if !sbplPathSafe(a) {
			continue
		}
		for _, d := range denied {
			if within(a, d) {
				fmt.Fprintf(&b, "(allow file-read* file-write* (subpath \"%s\"))\n", a)
				break
			}
		}
	}
	return b.String()
}

// tccApplies reports whether this launch gets the deny: the manager switch is
// on, the host is darwin, the role has not opted out, and sandbox-exec is there.
func (m *Manager) tccApplies(role api.Role) bool {
	if !m.TCCDeny || m.goos() != "darwin" || !role.TCCDenied() {
		return false
	}
	info, err := os.Stat(m.sandboxExecPath())
	return err == nil && info.Mode().IsRegular()
}

// tccBlock is the deny block for sess, read at launch: the network mounts are
// whatever the mount table holds now, so a mount made later is covered by the
// next start, not this one.
func (m *Manager) tccBlock(sess *api.Session) (string, error) {
	home, err := m.realHome()
	if err != nil || !filepath.IsAbs(home) {
		return "", fmt.Errorf("the home of the user marvel runs as is not known")
	}
	mounts := m.NetMounts
	if mounts == nil {
		mounts = networkMounts
	}
	return tccDenyBlock(home, mounts(), []string{sess.WorkDir}), nil
}

// stageTCCOnly stages the deny alone, for a role that names no profile of its
// own. It returns nil, with a log line, when the profile cannot be staged.
func (m *Manager) stageTCCOnly(sess *api.Session) *sandboxPlan {
	block, err := m.tccBlock(sess)
	if err != nil {
		sess.TCCDenyNote = err.Error()
		log.Printf("session %s: privacy-folder deny not applied: %v", sess.Key(), err)
		return nil
	}
	plan, fail := m.writeSandboxCopy(sess, []byte("(version 1)\n(allow default)\n"+block), func(format string, a ...any) *sandboxFailure {
		return &sandboxFailure{reason: RefusalSandboxProfile, msg: fmt.Sprintf(format, a...)}
	})
	if fail != nil {
		sess.TCCDenyNote = fail.msg
		log.Printf("session %s: privacy-folder deny not applied: %s", sess.Key(), fail.msg)
		return nil
	}
	plan.tccOnly = true
	return plan
}

// wrapTCCOnly prefixes the command with sandbox-exec and the staged deny. It
// takes no parameters, since the block names its paths itself. A command that is
// not one simple command is started as it is, with a log line.
func (m *Manager) wrapTCCOnly(sess *api.Session, plan *launchPlan) {
	sb := plan.sandbox
	if issue := shapeIssue(plan.command, true); issue != "" {
		sess.TCCDenyNote = "the launch command holds " + issue
		log.Printf("session %s: privacy-folder deny not applied: the launch command holds %s", sess.Key(), issue)
		_ = os.Remove(sb.copyPath)
		plan.sandbox = nil
		return
	}
	plan.command = shellQuote(m.sandboxExecPath()) + " -f " + shellQuote(sb.copyPath) + " " + plan.command
}
