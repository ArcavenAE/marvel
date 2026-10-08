package runtime

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// codexTrust is how a codex seat's start directory was judged against the
// workspace's trusted_folders.
type codexTrust struct {
	// Root is the main checkout the seat's folder trust is keyed on, set only
	// when the seat is trusted.
	Root string
	// Reason says why a seat is untrusted: not listed, no git, git error, bare
	// repo, or symlink unresolved. Empty when trusted.
	Reason string
}

// resolveCodexTrust judges start against the listed folders. A listed path may
// begin with ~/ (against home) or be relative (against wsRoot); both sides are
// compared after symlinks are resolved, as codex does for its keys. It fails
// closed: anything it cannot establish is untrusted with a reason. A listed
// entry that cannot be resolved here is skipped, never matched.
func resolveCodexTrust(start string, listed []string, home, wsRoot string) codexTrust {
	root, reason := api.GitMainRoot(start)
	if reason != "" {
		return codexTrust{Reason: reason}
	}
	for _, entry := range listed {
		abs, err := api.ExpandTrustedFolder(entry, home, wsRoot)
		if err != nil {
			continue
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			continue
		}
		if real == root {
			return codexTrust{Root: root}
		}
	}
	return codexTrust{Reason: "not listed"}
}

// codexFolderTrust decides how a seat starting in start is seeded and, when the
// workspace lists any trusted folder, records the outcome. With nothing listed
// it is today's seed and records nothing.
func codexFolderTrust(ctx *LaunchContext, start string) codexTrust {
	listed := ctx.Workspace.TrustedFolders
	if len(listed) == 0 {
		return codexTrust{Reason: "no trusted folders listed"}
	}
	home, _ := os.UserHomeDir()
	got := resolveCodexTrust(start, listed, home, ctx.Workspace.Root)
	msg := fmt.Sprintf("codex seat %s seeded untrusted for %s: %s", ctx.Session.Key(), start, got.Reason)
	if got.Root != "" {
		msg = fmt.Sprintf("codex seat %s seeded trusted for %s", ctx.Session.Key(), got.Root)
	}
	events.Emit(ctx.Events, events.Event{
		Kind: events.KindCodexTrust, Severity: events.SeverityInfo,
		Workspace: ctx.Session.Workspace, Team: ctx.Session.Team, Role: ctx.Session.Role, Session: ctx.Session.Name,
		Message: msg,
	})
	return got
}
