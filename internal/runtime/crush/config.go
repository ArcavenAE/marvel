// Package crush holds marvel's guards for launching the Crush harness. It
// carries Guard 1 of finding-033: refuse a launch whose workspace holds a
// Crush config file the operator has not approved, because Crush runs every
// one of them as trusted code.
//
// Nothing calls it yet. The Crush adapter (aae-orc-6c2r) does not exist, and
// until it lands this package is dead code that a test suite exercises.
// What the guard does not cover, so the adapter does not assume it does:
//
//   - The hash covers the entry file only. An approved config can source
//     other files, and those run unapproved (the direnv weakness).
//   - Crush also loads a user config from CRUSH_GLOBAL_CONFIG (by default a
//     crush directory under the user's config home) and a system config. Config that runs in a workspace
//     can write the user config and poison every later launch, so the adapter
//     must point CRUSH_GLOBAL_CONFIG at a location the agent cannot write, or
//     pin it too. This package scans workspaces only.
//   - A config file owned by another user is skipped by Crush and reported
//     here, and on a case-sensitive filesystem a differently-cased name that
//     Crush would not load is reported too. Both err toward refusing.
//   - The check is made at one moment. The adapter runs it as close to spawn
//     as it can, on a workspace marvel is not writing.
//   - An approved but malicious config, and any untrusted workspace, are out
//     of scope: approval is the operator vouching, and the untrusted tier
//     needs a sandbox.
//
// Guards 0 and 2 (the observability socket stays off, and no credential rides
// in the spawn environment) belong to the adapter and land with it.
package crush

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
)

// PinnedTag is the Crush release whose config search this package copies. Its
// walk was read at this tag and found unchanged at v0.98.1. A later release
// can add a config name or move the walk boundary, and either one is a bypass,
// so the adapter must re-read lookupConfigs before it supports a new release.
const PinnedTag = "v0.88.1"

// ConfigNames are the project config files Crush looks for in each directory,
// highest priority first. All four are executable: crushrc and .crushrc run in
// Crush's shell interpreter, and the JSON files are trusted code too
// (crush#3410).
var ConfigNames = []string{".crushrc", "crushrc", ".crush.json", "crush.json"}

// Reason says why a config file was refused. The empty Reason is approval.
type Reason string

const (
	// ReasonUnapproved is a config file with no entry in the approved set.
	ReasonUnapproved Reason = "unapproved"
	// ReasonChanged is a config file whose hash differs from the approved one.
	ReasonChanged Reason = "changed"
	// ReasonSymlink is a config file that is a symbolic link. It is refused
	// whatever it points at, since the target can be swapped after approval.
	ReasonSymlink Reason = "symlink"
	// ReasonNotRegular is a config file that is not a regular file.
	ReasonNotRegular Reason = "not-regular"
)

// File is one config file the scan found.
type File struct {
	// Rel is the file's path relative to the boundary, slash separated, with
	// the name as it is spelled on disk. It is the key into the approved set.
	Rel string
	// SHA256 is the hex digest of the file's bytes, empty for a refused link
	// or non-regular file, which are never read.
	SHA256 string
	// Reason is empty when the file is approved.
	Reason Reason
}

// Verdict is what a scan found.
type Verdict struct {
	Files []File
}

// OK reports whether every config file found is approved. A scan that found
// none is OK.
func (v Verdict) OK() bool { return len(v.Refused()) == 0 }

// Refused returns the files that are not approved.
func (v Verdict) Refused() []File {
	var out []File
	for _, f := range v.Files {
		if f.Reason != "" {
			out = append(out, f)
		}
	}
	return out
}

// Boundary returns the directory Crush stops its upward config search at: the
// git working tree root of cwd, or cwd itself when git cannot name one. It
// asks git the way Crush does, so the two agree.
func Boundary(cwd string) (string, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", cwd, err)
	}
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return abs, nil
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return abs, nil
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return abs, nil
	}
	return root, nil
}

// InspectConfig scans cwd and each ancestor up to and including boundary for
// Crush config files, and checks each against approved, a map from Rel to the
// hex sha256 the operator vouched for. An empty boundary scans cwd only. An
// error means the scan could not finish, and the caller must refuse the
// launch.
func InspectConfig(cwd, boundary string, approved map[string]string) (Verdict, error) {
	start, err := filepath.Abs(cwd)
	if err != nil {
		return Verdict{}, fmt.Errorf("resolve %s: %w", cwd, err)
	}
	if _, err := os.Stat(start); err != nil {
		return Verdict{}, fmt.Errorf("working directory: %w", err)
	}
	stop := start
	if boundary != "" {
		if stop, err = filepath.Abs(boundary); err != nil {
			return Verdict{}, fmt.Errorf("resolve %s: %w", boundary, err)
		}
	}
	canonStop := canonical(stop)

	// The walk, as Crush makes it: the directory itself, then each parent,
	// ending after the boundary, or at the filesystem root if it is never met.
	dirs := []string{start}
	for d := start; canonical(d) != canonStop; {
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		dirs = append(dirs, parent)
		d = parent
	}

	var v Verdict
	for i, d := range dirs {
		// Rel is measured from the last directory walked, so a boundary
		// spelled through a link and a cwd spelled another way still agree.
		var parts []string
		for j := len(dirs) - 2; j >= i; j-- {
			parts = append(parts, filepath.Base(dirs[j]))
		}
		files, err := scanDir(d, filepath.Join(parts...), approved)
		if err != nil {
			return Verdict{}, err
		}
		v.Files = append(v.Files, files...)
	}
	return v, nil
}

func scanDir(dir, relDir string, approved map[string]string) ([]File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var files []File
	for _, e := range entries {
		if !isConfigName(e.Name()) {
			continue
		}
		rel := filepath.ToSlash(filepath.Join(relDir, e.Name()))
		f := File{Rel: rel}
		info, err := os.Lstat(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", rel, err)
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			f.Reason = ReasonSymlink
		case !info.Mode().IsRegular():
			f.Reason = ReasonNotRegular
		default:
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", rel, err)
			}
			sum := sha256.Sum256(data)
			f.SHA256 = hex.EncodeToString(sum[:])
			want, ok := approved[rel]
			switch {
			case !ok:
				f.Reason = ReasonUnapproved
			case !strings.EqualFold(want, f.SHA256):
				f.Reason = ReasonChanged
			}
		}
		files = append(files, f)
	}
	return files, nil
}

// canonical resolves symbolic links so two spellings of one directory compare
// equal. A path that cannot be resolved is compared as written, as Crush does.
func canonical(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func isConfigName(name string) bool {
	for _, n := range ConfigNames {
		if foldEqual(name, n) {
			return true
		}
	}
	return false
}

// foldEqual compares two names the way a case-insensitive filesystem does,
// rune by rune across Unicode case-folding orbits, so the long s matches s.
// Every config name is ASCII, so no normalization form can make a different
// spelling equal to one.
func foldEqual(a, b string) bool {
	ra, rb := []rune(a), []rune(b)
	if len(ra) != len(rb) {
		return false
	}
	for i := range ra {
		if ra[i] == rb[i] {
			continue
		}
		matched := false
		for r := unicode.SimpleFold(ra[i]); r != ra[i]; r = unicode.SimpleFold(r) {
			if r == rb[i] {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}
