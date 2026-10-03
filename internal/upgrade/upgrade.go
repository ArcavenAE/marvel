// Package upgrade implements self-update for the marvel binary.
// Detects install method (Homebrew or direct binary) and delegates
// accordingly.
package upgrade

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	repoOwner = "ArcavenAE"
	repoName  = "marvel"
	// releasesPerPage is the page size asked of the GitHub releases API (its maximum).
	releasesPerPage = 100
	// maxReleasePages bounds a lookup for one exact tag.
	maxReleasePages = 20
)

// apiBase is the releases API root; tests point it at a local server.
var apiBase = "https://api.github.com/repos/" + repoOwner + "/" + repoName

type githubRelease struct {
	TagName     string        `json:"tag_name"`
	Prerelease  bool          `json:"prerelease"`
	PublishedAt string        `json:"published_at"`
	Assets      []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// installMethod describes how marvel was installed.
type installMethod int

const (
	methodDirect   installMethod = iota
	methodHomebrew               // Homebrew on macOS
	methodPackage                // Linux package manager
)

// Run performs the upgrade.
func Run(channel, targetVersion string) error {
	method := detectInstallMethod()

	switch method {
	case methodHomebrew:
		return upgradeViaHomebrew(channel, targetVersion)
	case methodPackage:
		fmt.Println("marvel was installed via a system package manager.")
		fmt.Println("Use your package manager to upgrade (e.g., apt upgrade, dnf upgrade).")
		return nil
	default:
		return upgradeDirectBinary(channel, targetVersion)
	}
}

func detectInstallMethod() installMethod {
	exe, err := os.Executable()
	if err != nil {
		return methodDirect
	}

	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		resolved = exe
	}

	if strings.Contains(resolved, "/Cellar/") || strings.Contains(resolved, "/homebrew/") {
		return methodHomebrew
	}

	if runtime.GOOS == "linux" {
		if isOwnedByPackageManager(resolved) {
			return methodPackage
		}
	}

	return methodDirect
}

func isOwnedByPackageManager(path string) bool {
	for _, cmd := range [][]string{
		{"dpkg", "-S", path},
		{"rpm", "-qf", path},
		{"apk", "info", "--who-owns", path},
	} {
		if exec.Command(cmd[0], cmd[1:]...).Run() == nil {
			return true
		}
	}
	return false
}

func upgradeViaHomebrew(channel, targetVersion string) error {
	// Note: marvel currently ships a single homebrew formula (`marvel`)
	// covering both alpha and stable builds — goreleaser publishes
	// alpha-tagged bottles to the same formula. The ArcavenAE/tap
	// does not have a `marvel-a` formula. If/when alpha gets split
	// into its own tap formula (the pattern used by forestage-a,
	// threedoors-a, jr-a), route the alpha channel back here.
	// The `channel` parameter is retained for that future fork.
	_ = channel
	formula := "arcavenae/tap/marvel"

	fmt.Printf("Installed via Homebrew. Running: brew upgrade %s\n", formula)

	// Update tap first to get latest formula.
	update := exec.Command("brew", "update")
	update.Stdout = os.Stdout
	update.Stderr = os.Stderr
	if err := update.Run(); err != nil {
		return fmt.Errorf("brew update failed: %w", err)
	}

	if err := checkBrewVersion(channel, targetVersion); err != nil {
		return err
	}

	// Upgrade the formula.
	cmd := exec.Command("brew", "upgrade", formula)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// brew upgrade exits non-zero if already up to date.
		fmt.Println("Already up to date (or brew upgrade returned an error).")
		return nil
	}

	fmt.Println("Upgrade complete.")
	return nil
}

func upgradeDirectBinary(channel, targetVersion string) error {
	fmt.Println("Checking for updates...")

	releases, err := fetchReleases(apiBase, targetVersion)
	if err != nil {
		return err
	}

	release, err := findRelease(releases, channel, targetVersion)
	if err != nil {
		return err
	}

	assetName, err := platformAssetName()
	if err != nil {
		return err
	}

	var asset *githubAsset
	for i := range release.Assets {
		if release.Assets[i].Name == assetName {
			asset = &release.Assets[i]
			break
		}
	}
	if asset == nil {
		available := make([]string, 0, len(release.Assets))
		for _, a := range release.Assets {
			available = append(available, a.Name)
		}
		return fmt.Errorf("no asset %q in release %s (available: %s)",
			assetName, release.TagName, strings.Join(available, ", "))
	}

	return downloadAndReplace(asset, release.TagName)
}

// fetchReleases lists releases. With no want it reads the first page, which
// is enough to find the latest. With a want it pages until the listing holds
// that exact tag or ends, so an older build beyond the first page is found.
func fetchReleases(base, want string) ([]githubRelease, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	var all []githubRelease
	for page := 1; page <= maxReleasePages; page++ {
		batch, err := fetchReleasePage(client, base, page)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if want == "" || len(batch) < releasesPerPage {
			break
		}
		if holdsTag(batch, want) {
			break
		}
	}
	return all, nil
}

func holdsTag(releases []githubRelease, tag string) bool {
	for i := range releases {
		if releases[i].TagName == tag {
			return true
		}
	}
	return false
}

func fetchReleasePage(client *http.Client, base string, page int) ([]githubRelease, error) {
	url := fmt.Sprintf("%s/releases?per_page=%d&page=%d", base, releasesPerPage, page)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "marvel-updater")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var releases []githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("parse releases: %w", err)
	}
	return releases, nil
}

func findRelease(releases []githubRelease, channel, targetVersion string) (*githubRelease, error) {
	prefix := "alpha-"
	if channel == "stable" {
		prefix = "stable-"
	}

	if targetVersion != "" {
		// Exact tag only. A partial match used to resolve to the first release
		// that contained the text, which may be a different build (marvel#485).
		for i := range releases {
			if releases[i].TagName == targetVersion {
				return &releases[i], nil
			}
		}
		return nil, fmt.Errorf("no release tagged %q (looked through %d releases; --version takes an exact tag)",
			targetVersion, len(releases))
	}

	// Find latest for channel.
	var best *githubRelease
	for i := range releases {
		r := &releases[i]
		if !strings.HasPrefix(r.TagName, prefix) {
			continue
		}
		if best == nil || r.PublishedAt > best.PublishedAt {
			best = r
		}
	}

	// Also check for v* stable tags.
	if channel == "stable" {
		for i := range releases {
			r := &releases[i]
			if strings.HasPrefix(r.TagName, "v") && !r.Prerelease {
				if best == nil || r.PublishedAt > best.PublishedAt {
					best = r
				}
			}
		}
	}

	if best == nil {
		return nil, fmt.Errorf("no %s releases found", channel)
	}
	return best, nil
}

func platformAssetName() (string, error) {
	goos := runtime.GOOS
	if goos == "darwin" {
		goos = "darwin"
	}

	goarch := runtime.GOARCH
	switch goarch {
	case "amd64":
		// keep as-is
	case "arm64":
		// keep as-is
	default:
		return "", fmt.Errorf("unsupported architecture: %s", goarch)
	}

	return fmt.Sprintf("marvel-%s-%s", goos, goarch), nil
}

func downloadAndReplace(asset *githubAsset, tag string) error {
	currentExe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine binary path: %w", err)
	}

	resolved, err := filepath.EvalSymlinks(currentExe)
	if err != nil {
		return fmt.Errorf("cannot resolve binary path: %w", err)
	}

	dir := filepath.Dir(resolved)
	base := filepath.Base(resolved)

	// Check write access.
	testPath := filepath.Join(dir, ".marvel-update-test")
	if err := os.WriteFile(testPath, []byte("test"), 0o644); err != nil {
		return fmt.Errorf("cannot write to %s (try running with sudo): %w", dir, err)
	}
	_ = os.Remove(testPath)

	newPath := filepath.Join(dir, base+".new")
	oldPath := filepath.Join(dir, base+".old")

	fmt.Printf("Downloading %s (%s)...\n", asset.Name, tag)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(asset.BrowserDownloadURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read download: %w", err)
	}

	if len(body) == 0 {
		return fmt.Errorf("downloaded file is empty")
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

	_ = os.Remove(oldPath)

	fmt.Printf("Upgraded to %s\n", tag)
	return nil
}

// buildKey reduces a release tag or a Homebrew formula version to the part
// that names the build. The tap spells it 0.1.0-alpha.20261002.232554.bc327df
// and GitHub spells it alpha-20261002-232554-bc327df, so both are split on
// dots and dashes and compared from the channel word onward.
func buildKey(v string) string {
	v = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), "v")
	parts := strings.FieldsFunc(v, func(r rune) bool { return r == '-' || r == '.' })
	for i, p := range parts {
		if p == "alpha" {
			parts = parts[i:]
			break
		}
	}
	return strings.Join(parts, ".")
}

// sameBuild reports whether a release tag and a Homebrew formula version name
// the same build. An empty or partial tag names none.
func sameBuild(tag, formulaVersion string) bool {
	k := buildKey(tag)
	return k != "" && k == buildKey(formulaVersion)
}

// brewFormula is the part of `brew info --json=v2` that --version needs.
type brewFormula struct {
	Versions struct {
		Stable string `json:"stable"`
	} `json:"versions"`
	VersionedFormulae []string `json:"versioned_formulae"`
}

func brewInfo(formula string) (brewFormula, error) {
	cmd := exec.Command("brew", "info", "--json=v2", formula)
	cmd.Stderr = os.Stderr
	data, err := cmd.Output()
	if err != nil {
		return brewFormula{}, fmt.Errorf("brew info %s: %w", formula, err)
	}
	var doc struct {
		Formulae []brewFormula `json:"formulae"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return brewFormula{}, fmt.Errorf("parse brew info %s: %w", formula, err)
	}
	if len(doc.Formulae) != 1 || doc.Formulae[0].Versions.Stable == "" {
		return brewFormula{}, fmt.Errorf("brew info %s: no formula version in the output", formula)
	}
	return doc.Formulae[0], nil
}

// checkBrewVersion refuses a --version the tap's formula cannot install
// exactly. brew installs only the formula's current version, so the request
// must name that build. Anything marvel cannot confirm is refused: a pin that
// is silently ignored defeats the reason to pin (marvel#485).
func checkBrewVersion(channel, targetVersion string) error {
	if targetVersion == "" {
		return nil
	}
	const formula = "arcavenae/tap/marvel"
	info, err := brewInfo(formula)
	if err != nil {
		return fmt.Errorf("--version %s refused: marvel is installed with Homebrew (channel %s) "+
			"and cannot confirm what the tap installs: %w", targetVersion, channel, err)
	}
	if sameBuild(targetVersion, info.Versions.Stable) {
		return nil
	}
	versioned := formula + "@" + targetVersion
	for _, name := range info.VersionedFormulae {
		if name == versioned {
			return fmt.Errorf("--version %s refused: marvel is installed with Homebrew (channel %s), "+
				"and marvel upgrade installs only the tap's current formula %s; "+
				"the tap has a formula for that build, so install it with: brew install %s",
				targetVersion, channel, info.Versions.Stable, versioned)
		}
	}
	return fmt.Errorf("--version %s refused: marvel is installed with Homebrew (channel %s), "+
		"and the tap's formula installs only %s; "+
		"run marvel upgrade without --version to take that build, "+
		"or install the exact build from its release at https://github.com/%s/%s/releases/tag/%s",
		targetVersion, channel, info.Versions.Stable, repoOwner, repoName, targetVersion)
}

// RefuseDev refuses to self-upgrade a dev build. A dev build is one made from
// source with no version stamped in; upgrading it would overwrite a local
// build with a release.
func RefuseDev(current string) error {
	if current != "dev" {
		return nil
	}
	return errors.New("this is a dev build (channel dev), so marvel upgrade would overwrite your local build with a release; " +
		"rebuild from source (go build ./cmd/marvel), or install a release with Homebrew or from the releases page")
}
