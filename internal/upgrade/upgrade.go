// Package upgrade implements self-update for the marvel binary.
// Detects install method (Homebrew or direct binary) and delegates
// accordingly.
package upgrade

import (
	"encoding/json"
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
	apiBase   = "https://api.github.com/repos/" + repoOwner + "/" + repoName
)

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
		return upgradeViaHomebrew(channel)
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

func upgradeViaHomebrew(channel string) error {
	formula := "ArcavenAE/tap/marvel"
	if channel == "alpha" {
		formula = "ArcavenAE/tap/marvel-a"
	}

	fmt.Printf("Installed via Homebrew. Running: brew upgrade %s\n", formula)

	// Update tap first to get latest formula.
	update := exec.Command("brew", "update")
	update.Stdout = os.Stdout
	update.Stderr = os.Stderr
	if err := update.Run(); err != nil {
		return fmt.Errorf("brew update failed: %w", err)
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

	releases, err := fetchReleases()
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

func fetchReleases() ([]githubRelease, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", apiBase+"/releases", nil)
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
		for i := range releases {
			if releases[i].TagName == targetVersion {
				return &releases[i], nil
			}
		}
		// Partial match.
		for i := range releases {
			if strings.Contains(releases[i].TagName, targetVersion) {
				return &releases[i], nil
			}
		}
		return nil, fmt.Errorf("no release matching %q", targetVersion)
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
