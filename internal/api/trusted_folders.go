package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// The reasons GitMainRoot gives for not finding a root. They are the words an
// event and an operator read, so they are fixed.
const (
	GitReasonNoGit             = "no git"
	GitReasonError             = "git error"
	GitReasonBare              = "bare repo"
	GitReasonSymlinkUnresolved = "symlink unresolved"
)

// gitTimeout bounds one git call. The seed and apply paths must not hang on a
// stuck filesystem.
const gitTimeout = 5 * time.Second

// gitOut runs git in dir and returns its trimmed stdout. The inherited
// repository variables are dropped so the answer is about dir, not about the
// environment the daemon happened to start in.
func gitOut(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "GIT_DIR="), strings.HasPrefix(kv, "GIT_WORK_TREE="),
			strings.HasPrefix(kv, "GIT_COMMON_DIR="), strings.HasPrefix(kv, "GIT_INDEX_FILE="):
		default:
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && strings.Contains(string(ee.Stderr), "not a git repository") {
			return "", errNotARepo
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

var errNotARepo = errors.New("not a git repository")

// GitMainRoot is the main checkout's top-level for the repository start is in,
// the directory codex keys a folder's trust on: the parent of the git common
// directory, so every subdirectory and every linked worktree of one repository
// gives the same root, and a subrepo with its own .git gives its own. The root
// has symlinks resolved. When it cannot say, root is empty and reason is one of
// the GitReason constants.
func GitMainRoot(start string) (root, reason string) {
	if start == "" {
		return "", GitReasonError
	}
	if info, err := os.Stat(start); err != nil || !info.IsDir() {
		return "", GitReasonError
	}
	if _, err := exec.LookPath("git"); err != nil {
		return "", GitReasonNoGit
	}
	common, err := gitOut(start, "rev-parse", "--path-format=absolute", "--git-common-dir")
	switch {
	case errors.Is(err, errNotARepo):
		return "", GitReasonNoGit
	case err != nil || common == "":
		return "", GitReasonError
	}
	if filepath.Base(common) != ".git" {
		if bare, berr := gitOut(start, "rev-parse", "--is-bare-repository"); berr == nil && bare == "true" {
			return "", GitReasonBare
		}
		return "", GitReasonError
	}
	real, err := filepath.EvalSymlinks(filepath.Dir(common))
	if err != nil {
		return "", GitReasonSymlinkUnresolved
	}
	return real, ""
}

// ExpandTrustedFolder resolves one trusted_folders entry: a leading ~/ against
// home, a relative path against the workspace root, an absolute path as it is.
// It does not touch the filesystem.
func ExpandTrustedFolder(p, home, wsRoot string) (string, error) {
	switch {
	case p == "":
		return "", errors.New("is empty")
	case p == "~" || strings.HasPrefix(p, "~/"):
		if home == "" {
			return "", errors.New("starts with ~ and the home directory is unknown")
		}
		return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/")), nil
	case strings.HasPrefix(p, "~"):
		return "", errors.New("starts with ~ but is not ~/ (another user's home is not expanded)")
	case filepath.IsAbs(p):
		return filepath.Clean(p), nil
	case wsRoot == "":
		return "", errors.New("is relative and workspace.root is not set to resolve it against (marvel work sends the manifest's directory as workspace_root; a raw API post must set workspace.root or the workspace_root parameter)")
	default:
		return filepath.Join(wsRoot, p), nil
	}
}

// ValidateTrustedFolders is the daemon-side check at apply of the workspace's
// trusted_folders: each path must exist and be the top-level of a main git
// checkout (marvel#684). It asks whether the path is well formed, not whether it
// should be trusted. It runs after ValidateWorkDirs, so the workspace root a
// relative path resolves against is already normalized.
func (m *Manifest) ValidateTrustedFolders() error {
	if m.Workspace.TrustedFolders == nil {
		return nil
	}
	home, _ := os.UserHomeDir()
	for _, entry := range *m.Workspace.TrustedFolders {
		label := fmt.Sprintf("workspace.trusted_folders %q", entry)
		abs, err := ExpandTrustedFolder(entry, home, m.Workspace.Root)
		if err != nil {
			return fmt.Errorf("%s %w", label, err)
		}
		info, err := os.Stat(abs)
		switch {
		case os.IsNotExist(err):
			return fmt.Errorf("%s does not exist on this host (resolved to %s)", label, abs)
		case err != nil:
			return fmt.Errorf("%s: %w", label, err)
		case !info.IsDir():
			return fmt.Errorf("%s is not a directory (resolved to %s)", label, abs)
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		top, err := gitOut(real, "rev-parse", "--show-toplevel")
		if err != nil {
			return fmt.Errorf("%s is not a git repository", label)
		}
		if top, err = filepath.EvalSymlinks(top); err != nil || top != real {
			return fmt.Errorf("%s is not a git top-level (list %s)", label, top)
		}
		if root, reason := GitMainRoot(real); reason != "" {
			return fmt.Errorf("%s cannot be used: %s", label, reason)
		} else if root != real {
			return fmt.Errorf("%s is a linked worktree of %s; list the main checkout, which every worktree of it matches", label, root)
		}
	}
	return nil
}

// TrustedFolderNotes says when this apply's trusted_folders differs from the
// workspace's stored list, since several manifests can share one workspace and
// the last apply wins. An absent key and an unchanged list say nothing.
func (m *Manifest) TrustedFolderNotes(store *Store) []string {
	if m.Workspace.TrustedFolders == nil {
		return nil
	}
	ws, err := store.GetWorkspace(m.Workspace.Name)
	if err != nil {
		return nil
	}
	incoming := *m.Workspace.TrustedFolders
	if slices.Equal(ws.TrustedFolders, incoming) {
		return nil
	}
	return []string{fmt.Sprintf("workspace %s trusted_folders changes from %v to %v; manifests that share a workspace replace each other's list, and the last apply wins", ws.Name, ws.TrustedFolders, incoming)}
}
