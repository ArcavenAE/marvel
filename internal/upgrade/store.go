package upgrade

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/arcavenae/marvel/internal/paths"
)

// The version store (docs/design/zero-downtime-upgrade.md section 5). Each
// install channel's binaries stay where that channel puts them, and a small
// record under the marvel home names the active build and the prior one. The
// direct channel is the only one that destroys the build it replaces, so it
// alone keeps the prior, under versions/<version>/marvel. A Homebrew keg and a
// mise install are recorded where they are and never moved.
//
// Nothing reads the record yet. upgrade --check (aae-orc-c0nzn) and the
// fallback to the prior binary (aae-orc-n1mgg) are its readers.

// The install channels a recorded build came from.
const (
	channelDirect   = "direct"
	channelHomebrew = "homebrew"
	channelMise     = "mise"
)

// checksumsAsset is the release asset that lists the sha256 of the others, in
// sha256sum format. Releases before aae-orc-r770g do not publish it.
const checksumsAsset = "checksums.txt"

// Build is one marvel build the version store knows about.
type Build struct {
	// Version is the build's release tag for the direct channel, the keg's
	// directory name for Homebrew (the formula's spelling), and the running
	// binary's version for a prior that was not installed through the store.
	Version string `json:"version"`
	Channel string `json:"channel"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256,omitempty"`
	// Verified is true only when SHA256 matched the release's checksums.txt
	// when the build was installed. A build from a release that published no
	// checksums.txt, a build installed before the store existed, and a Homebrew
	// keg are recorded unverified.
	Verified bool `json:"verified"`
}

// Record names the active build and the one it replaced.
type Record struct {
	Active Build  `json:"active"`
	Prior  *Build `json:"prior,omitempty"`
}

// ReadRecord reads the version record under the layout's home. found is false
// when there is no record yet, which is not an error.
func ReadRecord(l paths.Layout) (rec Record, found bool, err error) {
	data, err := os.ReadFile(l.VersionRecord())
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("read version record: %w", err)
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		return Record{}, false, fmt.Errorf("parse version record %s: %w", l.VersionRecord(), err)
	}
	return rec, true, nil
}

// writeRecord replaces the version record. It writes a temporary file and
// renames it, so a reader sees the old record or the new one, never a part.
func writeRecord(l paths.Layout, rec Record) error {
	if err := l.EnsureVersionsDir(); err != nil {
		return fmt.Errorf("create the version store: %w", err)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encode version record: %w", err)
	}
	tmp, err := os.CreateTemp(l.VersionsDir(), ".record-*.json")
	if err != nil {
		return fmt.Errorf("write version record: %w", err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write version record: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write version record: %w", err)
	}
	if err := os.Rename(tmp.Name(), l.VersionRecord()); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write version record: %w", err)
	}
	return nil
}

// channelOf names the install channel a binary path belongs to. A path the
// install method calls direct can still be a mise install, which keeps each
// version in its own directory.
func channelOf(path string) string {
	switch {
	case strings.Contains(path, "/Cellar/") || strings.Contains(path, "/homebrew/"):
		return channelHomebrew
	case strings.Contains(path, "/mise/installs/"):
		return channelMise
	default:
		return channelDirect
	}
}

// sumBytes is the hex sha256 of data, the spelling checksums.txt uses.
func sumBytes(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

// parseChecksums finds name's sum in sha256sum output: a hex sum, then the
// file name, which is marked with '*' when it was hashed in binary mode.
func parseChecksums(data []byte, name string) (string, bool) {
	for line := range strings.SplitSeq(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		if strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0]), true
		}
	}
	return "", false
}

// verifyDownload checks a downloaded asset against the release's checksums.txt.
// A release that publishes none cannot be checked, so the download is
// unverified and that is not an error. A release that publishes one is held to
// it: a sum that differs, a list without this asset, or a list that cannot be
// read refuses the install, because a check that fails open is not a check.
func verifyDownload(client *http.Client, release *githubRelease, name string, body []byte) (bool, error) {
	var list *githubAsset
	for i := range release.Assets {
		if release.Assets[i].Name == checksumsAsset {
			list = &release.Assets[i]
			break
		}
	}
	if list == nil {
		return false, nil
	}
	resp, err := client.Get(list.BrowserDownloadURL)
	if err != nil {
		return false, fmt.Errorf("release %s publishes %s and it cannot be read, so the download cannot be verified; nothing was installed: %w",
			release.TagName, checksumsAsset, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("release %s publishes %s and reading it returned HTTP %d, so the download cannot be verified; nothing was installed",
			release.TagName, checksumsAsset, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("read %s of release %s: %w", checksumsAsset, release.TagName, err)
	}
	want, ok := parseChecksums(data, name)
	if !ok {
		return false, fmt.Errorf("release %s publishes %s with no line for %s, so the download cannot be verified; nothing was installed",
			release.TagName, checksumsAsset, name)
	}
	if got := sumBytes(body); got != want {
		return false, fmt.Errorf("the download of %s does not match release %s's %s (sha256 %s, the list says %s); nothing was installed",
			name, release.TagName, checksumsAsset, got, want)
	}
	return true, nil
}

// storeName is the directory a kept build lives in: its version when that is
// a plain name, and otherwise its sum, so a version string cannot name a path
// outside the store.
func storeName(version, sum string) string {
	if version != "" && version != "." && version != ".." && !strings.ContainsAny(version, `/\`) {
		return version
	}
	if len(sum) > 12 {
		sum = sum[:12]
	}
	return "sha256-" + sum
}

// inStore reports whether path is a kept build: <store>/<name>/marvel.
func inStore(l paths.Layout, path string) bool {
	if filepath.Base(path) != "marvel" {
		return false
	}
	rel, err := filepath.Rel(l.VersionsDir(), filepath.Dir(path))
	return err == nil && rel != "." && !strings.Contains(rel, string(filepath.Separator)) && rel != ".."
}

// keepPrior moves the build at old into the store and returns its new path.
// A rename across filesystems fails, so it then copies and removes the source.
func keepPrior(l paths.Layout, old, version, sum string) (string, error) {
	if err := l.EnsureVersionsDir(); err != nil {
		return "", fmt.Errorf("create the version store: %w", err)
	}
	dir := filepath.Join(l.VersionsDir(), storeName(version, sum))
	if err := os.MkdirAll(dir, paths.ModeDir); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	dst := filepath.Join(dir, "marvel")
	if err := os.Rename(old, dst); err == nil {
		return dst, nil
	}
	if err := copyFile(old, dst); err != nil {
		_ = os.Remove(dst)
		return "", err
	}
	if err := os.Remove(old); err != nil {
		_, _ = fmt.Fprintf(errOut, "note: the prior build was copied to %s and %s could not be removed: %v\n", dst, old, err)
	}
	return dst, nil
}

// copyFile copies src to dst as an executable only its owner can write.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer func() { _ = in.Close() }()
	o, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	if _, err := io.Copy(o, in); err != nil {
		_ = o.Close()
		return fmt.Errorf("copy %s to %s: %w", src, dst, err)
	}
	if err := o.Close(); err != nil {
		return fmt.Errorf("copy %s to %s: %w", src, dst, err)
	}
	return nil
}

// storeNote says something about the version store went wrong. The upgrade
// itself has already happened, so it is a note and not a failure.
func storeNote(format string, args ...any) {
	_, _ = fmt.Fprintf(errOut, "note: version store: "+format+"\n", args...)
}

// installDirect swaps body in for the binary at resolved, keeps the build it
// replaces in the store, and records both. A mise install is swapped as it
// always was and recorded, and its prior is not kept: mise keeps versions in
// directories of its own, and what upgrade does to a mise install is not this
// change's to decide.
func installDirect(l paths.Layout, resolved string, body []byte, tag, current string, verified bool) error {
	dir := filepath.Dir(resolved)
	base := filepath.Base(resolved)
	newPath := filepath.Join(dir, base+".new")
	oldPath := filepath.Join(dir, base+".old")

	priorSum, err := fingerprint(resolved)
	if err != nil {
		return fmt.Errorf("read the binary being replaced, %s: %w", resolved, err)
	}

	if err := os.WriteFile(newPath, body, 0o755); err != nil {
		return fmt.Errorf("write %s: %w", newPath, err)
	}

	// Atomic swap: current → .old, .new → current.
	if err := os.Rename(resolved, oldPath); err != nil {
		_ = os.Remove(newPath)
		return fmt.Errorf("backup current binary: %w", err)
	}

	if err := os.Rename(newPath, resolved); err != nil {
		_ = os.Rename(oldPath, resolved) // rollback
		_ = os.Remove(newPath)
		return fmt.Errorf("install new binary: %w", err)
	}

	channel := channelOf(resolved)
	rec, found, err := ReadRecord(l)
	if err != nil {
		storeNote("%v; writing a new record", err)
		found = false
	}

	var prior *Build
	if channel == channelMise {
		_ = os.Remove(oldPath)
	} else {
		stored, err := keepPrior(l, oldPath, current, priorSum)
		if err != nil {
			storeNote("the prior build could not be kept (%v); it is left at %s", err, oldPath)
			stored = oldPath
		}
		prior = &Build{
			Version:  current,
			Channel:  channel,
			Path:     stored,
			SHA256:   priorSum,
			Verified: found && rec.Active.SHA256 == priorSum && rec.Active.Verified,
		}
		// The store keeps one prior. The one before it is dropped, and only
		// from inside the store, whatever the record says.
		if found && rec.Prior != nil && rec.Prior.Path != stored && inStore(l, rec.Prior.Path) {
			if err := os.RemoveAll(filepath.Dir(rec.Prior.Path)); err != nil {
				storeNote("the older prior build %s could not be removed: %v", rec.Prior.Path, err)
			}
		}
	}

	active := Build{Version: tag, Channel: channel, Path: resolved, SHA256: sumBytes(body), Verified: verified}
	if err := writeRecord(l, Record{Active: active, Prior: prior}); err != nil {
		storeNote("%v", err)
	}
	return nil
}

// kegVersion is the version directory of a Homebrew keg binary,
// <Cellar>/marvel/<version>/bin/marvel, or the empty string for another path.
func kegVersion(path string) string {
	bin := filepath.Dir(path)
	if filepath.Base(bin) != "bin" {
		return ""
	}
	return filepath.Base(filepath.Dir(bin))
}

// recordKeg records a Homebrew upgrade: the keg brew made active, and the keg
// it replaced when that still exists. Nothing is moved. brew's cleanup can
// remove the old keg, and a path that is gone is not recorded as a prior.
func recordKeg(l paths.Layout, before, after string) {
	rec, found, err := ReadRecord(l)
	if err != nil {
		storeNote("%v; writing a new record", err)
		found = false
	}
	sum, err := fingerprint(after)
	if err != nil {
		storeNote("the installed keg %s cannot be read: %v", after, err)
		return
	}
	next := Record{Active: Build{Version: kegVersion(after), Channel: channelHomebrew, Path: after, SHA256: sum}}
	switch {
	case before != after:
		if priorSum, err := fingerprint(before); err == nil {
			next.Prior = &Build{Version: kegVersion(before), Channel: channelHomebrew, Path: before, SHA256: priorSum}
		}
	case found:
		next.Prior = rec.Prior
	}
	if err := writeRecord(l, next); err != nil {
		storeNote("%v", err)
	}
}
